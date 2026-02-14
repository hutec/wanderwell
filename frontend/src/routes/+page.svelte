<script lang="ts">
	import RouteList from '$lib/RouteList.svelte';
	import { routesState } from '$lib/state.svelte';
	import { getBaseStyle } from '$lib/mapStyle.svelte';
	import { tileServerEndpoint } from '$lib/config';
	import { checkAuth, login, logout, authState } from '$lib/auth.svelte';

	import { MapLibre } from 'svelte-maplibre-gl';
	import type { StyleSpecification } from 'maplibre-gl';

	// Check authentication on page load
	$effect(() => {
		checkAuth();
	});

	// Reactive map style that updates when user changes
	let mapStyle = $state<StyleSpecification | null>(null);

	// Load base map style once
	$effect(() => {
		getBaseStyle().then((style) => {
			mapStyle = style as StyleSpecification;
		});
	});

	// Derived style that updates AllRoutes URL and filters routes when user/selection changes
	let activeMapStyle = $derived.by(() => {
		if (!mapStyle) return null;

		// Deep clone the base style to avoid mutations
		const style = JSON.parse(JSON.stringify(mapStyle));

		// Update the AllRoutes source URL based on userID
		if (authState.isAuthenticated && style.sources && style.sources.AllRoutes) {
			style.sources.AllRoutes.url = tileServerEndpoint(`/data/${authState.currentUser?.id}.json`);
		}

		// Add filter to Route layer based on selected route IDs
		if (style.layers) {
			const routeLayer = style.layers.find((layer) => layer.id === 'Route');
			if (routeLayer && routesState.routes.length > 0) {
				routeLayer.filter = [
					'all',
					['in', 'id', ...routesState.routes.map((routeId) => String(routeId))]
				];
			} else if (routeLayer) {
				delete routeLayer.filter;
			}
		}

		return style;
	});
</script>

<div class="flex h-screen w-full bg-slate-100 text-slate-900">
	<aside class="flex h-full w-80 shrink-0 flex-col border-r border-slate-200 bg-white shadow-sm">
		<div class="border-b border-slate-200 px-4 py-3">
			<h1 class="text-base font-semibold tracking-tight">Wanderwell</h1>
			<p class="mt-0.5 text-xs text-slate-500">Route explorer</p>
		</div>

		<div class="flex min-h-0 flex-1 flex-col p-4">
			{#if authState.isAuthenticated}
				<div class="mb-4 flex items-start justify-between gap-3">
					<p class="text-sm text-slate-600">
						Logged in as
						<span class="font-semibold text-slate-900">{authState.currentUser?.name}</span>
					</p>
					<button
						type="button"
						class="rounded-md border border-rose-200 bg-rose-50 px-3 py-1.5 text-sm font-medium text-rose-700 transition hover:bg-rose-100 focus:ring-2 focus:ring-rose-400 focus:ring-offset-2 focus:outline-none"
						onclick={logout}
					>
						Logout
					</button>
				</div>

				<div class="min-h-0 flex-1">
					<RouteList userID={authState.currentUser?.id} />
				</div>
			{:else}
				<div class="flex h-full items-start justify-end">
					<button
						type="button"
						class="rounded-md border border-emerald-200 bg-emerald-50 px-3 py-1.5 text-sm font-medium text-emerald-700 transition hover:bg-emerald-100 focus:ring-2 focus:ring-emerald-400 focus:ring-offset-2 focus:outline-none"
						onclick={login}
					>
						Login
					</button>
				</div>
			{/if}
		</div>
	</aside>

	<main class="min-h-0 flex-1">
		{#if activeMapStyle}
			<MapLibre class="h-full w-full" style={activeMapStyle} />
		{:else}
			<div class="flex h-full w-full items-center justify-center bg-slate-200">
				<p class="text-sm text-slate-600">Loading map...</p>
			</div>
		{/if}
	</main>
</div>
