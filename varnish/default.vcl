vcl 4.1;

backend vector_tileserver {
    .host = "vector-tileserver";
    .port = "3000";
}

backend raster_tileserver {
    .host = "raster-tileserver";
    .port = "4545";
}

# Allow BAN requests from localhost and all private (Docker) network ranges
acl purge_acl {
    "127.0.0.1";
    "::1";
    "10.0.0.0"/8;
    "172.16.0.0"/12;
    "192.168.0.0"/16;
}

sub vcl_recv {
    # Handle BAN requests from the backend to invalidate a user's tiles
    if (req.method == "BAN") {
        if (!client.ip ~ purge_acl) {
            return(synth(403, "Not allowed"));
        }
        if (!req.http.X-User-Id) {
            return(synth(400, "X-User-Id header required"));
        }
        ban("req.url ~ [?&]user_id=" + req.http.X-User-Id + "(&|$)");
        return(synth(200, "Ban added"));
    }

    # Keep the public service prefixes at Traefik and route them here.
    # Strip the prefix only after selecting the upstream.
    if (req.url ~ "^/raster(?:/|$)") {
        set req.backend_hint = raster_tileserver;
        set req.url = regsub(req.url, "^/raster", "");
        # The token is checked by ForwardAuth and is not needed upstream or in
        # the cache key.
        set req.url = regsuball(req.url, "([?&])token=[^&]*(&|$)", "\1");
        set req.url = regsub(req.url, "\?$", "");
    } else if (req.url ~ "^/vector(?:/|$)") {
        set req.backend_hint = vector_tileserver;
        set req.url = regsub(req.url, "^/vector", "");
    } else {
        return(synth(404, "Unknown tile service"));
    }

    if (req.method != "GET" && req.method != "HEAD") {
        return(pass);
    }

    return(hash);
}

sub vcl_hash {
    # Full URL already includes path (z/x/y) and user_id query param
    hash_data(req.url);
    hash_data(req.http.Host);
    return(lookup);
}

sub vcl_backend_response {
    if (beresp.status == 200) {
        set beresp.ttl = 1h;
        # Allow serving stale tiles while revalidating
        set beresp.grace = 5m;
    } else {
        # Don't cache errors
        set beresp.ttl = 0s;
        set beresp.uncacheable = true;
    }
    return(deliver);
}

sub vcl_deliver {
    # Add a header so clients can see cache hit/miss status
    if (obj.hits > 0) {
        set resp.http.X-Cache = "HIT";
    } else {
        set resp.http.X-Cache = "MISS";
    }
    return(deliver);
}
