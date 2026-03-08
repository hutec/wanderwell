<script lang="ts">
	import RouteList from '$lib/RouteList.svelte';
	import { routesState, loadRoutes } from '$lib/state.svelte';
	import { getBaseStyle } from '$lib/mapStyle.svelte';
	import { tileServerEndpoint } from '$lib/config';
	import { checkAuth, login, logout, authState } from '$lib/auth.svelte';
	import type { Route } from '$lib/types/route';

	import { MapLibre } from 'svelte-maplibre-gl';
	import type {
		StyleSpecification,
		Map as MapLibreMap,
		LngLatBounds as LngLatBoundsType
	} from 'maplibre-gl';
	import maplibregl from 'maplibre-gl';
	const { LngLatBounds } = maplibregl;

	$effect(() => {
		checkAuth();
	});

	$effect(() => {
		loadRoutes(authState.currentUser?.id);
	});

	let mapStyle = $state<StyleSpecification | null>(null);

	$effect(() => {
		getBaseStyle().then((style) => {
			mapStyle = style as StyleSpecification;
		});
	});

	let activeMapStyle = $derived.by(() => {
		if (!mapStyle) return null;

		const style = JSON.parse(JSON.stringify(mapStyle));

		if (authState.isAuthenticated && style.sources && style.sources.AllRoutes) {
			style.sources.AllRoutes.url = tileServerEndpoint(
				`/user_routes?user_id=${authState.currentUser?.id}`
			);
		}

		if (style.layers) {
			const selectedIds = routesState.routes.map((route) => route.id);
			const filter = ['all', ['in', 'id', ...selectedIds]];

			for (const layerId of ['Route', 'RouteArrows']) {
				const layer = style.layers.find((l: { id: string }) => l.id === layerId);
				if (layer && selectedIds.length > 0) {
					layer.filter = filter;
				} else if (layer) {
					delete layer.filter;

					// If no routes are selected, hide the arrow layer
					if (layerId == 'RouteArrows') {
						layer.layout = {
							...layer.layout,
							visibility: 'none'
						};
					}
				}
			}
		}

		return style;
	});

	let isSidebarOpen = $state(true);
	let map = $state<MapLibreMap | undefined>(undefined);

	$effect(() => {
		if (!map) return;

		let popup: maplibregl.Popup | null = null;

		const handleRouteClick = (
			e: maplibregl.MapMouseEvent & { features?: maplibregl.MapGeoJSONFeature[] }
		) => {
			const features = e.features;
			if (!features?.length) return;

			const html = features
				.map((feature) => {
					const { name, start_date, distance } = feature.properties as {
						name: string;
						start_date: string;
						distance: number;
					};
					const formattedDate = start_date
						? new Date(start_date).toLocaleDateString(undefined, {
								year: 'numeric',
								month: 'long',
								day: 'numeric'
							})
						: 'Unknown date';
					const formattedDistance =
						distance != null ? `${distance.toFixed(1)} km` : 'Unknown distance';
					return `<strong>${name}</strong><br/><span>${formattedDate}</span><br/><span>${formattedDistance}</span>`;
				})
				.join('<hr/>');

			popup?.remove();
			popup = new maplibregl.Popup({ closeButton: true, closeOnClick: false })
				.setLngLat(e.lngLat)
				.setHTML(html)
				.addTo(map!);
		};

		const setCursorPointer = () => {
			map!.getCanvas().style.cursor = 'pointer';
		};
		const resetCursor = () => {
			map!.getCanvas().style.cursor = '';
		};

		map.on('click', 'Route', handleRouteClick);
		map.on('mouseenter', 'Route', setCursorPointer);
		map.on('mouseleave', 'Route', resetCursor);

		return () => {
			map!.off('click', 'Route', handleRouteClick);
			map!.off('mouseenter', 'Route', setCursorPointer);
			map!.off('mouseleave', 'Route', resetCursor);
			popup?.remove();
		};
	});

	const parseBounds = (bounds: string): LngLatBoundsType => {
		// Input is LatLng bounds in the format "lat1,lng1,lat2,lng2"
		if (bounds === '') return new LngLatBounds();

		const boundsArr = bounds.split(',').map((bound) => Number(bound));
		return new LngLatBounds([boundsArr[1], boundsArr[0]], [boundsArr[3], boundsArr[2]]);
	};

	const getBounds = (): LngLatBoundsType => {
		return routesState.routes.reduce(
			(bounds: LngLatBoundsType, route: Route) => bounds.extend(parseBounds(route.bounds)),
			new LngLatBounds()
		);
	};

	const snapToSelection = () => {
		if (!map || routesState.routes.length === 0) return;

		const bounds = getBounds();
		if (bounds.isEmpty()) return;

		map.fitBounds(bounds, {
			padding: 48,
			duration: 600
		});
	};
</script>

<div class="relative flex h-screen w-full overflow-hidden bg-slate-100 text-slate-900">
	<aside
		class="absolute inset-y-0 left-0 z-30 flex h-full w-80 max-w-[85vw] shrink-0 flex-col border-r border-slate-200 bg-white shadow-sm md:relative"
		class:hidden={!isSidebarOpen}
	>
			<div class="flex items-center justify-between border-b border-slate-200 px-4 py-3">
				<div>
					<h1 class="text-base font-semibold tracking-tight">Wanderwell</h1>
					<p class="mt-0.5 text-xs text-slate-500">Route explorer</p>
				</div>
				<button
					type="button"
					class="rounded-md border border-slate-200 bg-white p-2 text-slate-600 transition hover:bg-slate-50 hover:text-slate-900 focus:ring-2 focus:ring-slate-400 focus:ring-offset-2 focus:outline-none"
					aria-label="Collapse sidebar"
					onclick={() => (isSidebarOpen = false)}
				>
					✕
				</button>
			</div>

			<div class="flex min-h-0 flex-1 flex-col p-4">
				{#if authState.isAuthenticated}
					<div class="mb-4 flex items-start justify-between gap-3">
						<p class="text-sm text-slate-600">
							Logged in as
							<span class="font-semibold text-slate-900">{authState.currentUser?.firstname}</span>
						</p>
						<button
							type="button"
							class="rounded-md border border-rose-200 bg-rose-50 px-3 py-1.5 text-sm font-medium text-rose-700 transition hover:bg-rose-100 focus:ring-2 focus:ring-rose-400 focus:ring-offset-2 focus:outline-none"
							onclick={logout}
						>
							Logout
						</button>
					</div>
					<div class="mb-4 flex gap-3">
						<button
							type="button"
							class="self-start rounded-md border border-amber-300 bg-amber-100 px-3 py-1.5 text-sm font-medium text-amber-900 transition hover:bg-amber-200"
							onclick={() => (routesState.routes = [])}
						>
							Reset selection
						</button>
						<button
							type="button"
							class="self-start rounded-md border border-amber-300 bg-amber-100 px-3 py-1.5 text-sm font-medium text-amber-900 transition hover:bg-amber-200"
							onclick={snapToSelection}
						>
							Snap to selection
						</button>
					</div>
					<div class="min-h-0 flex-1">
						<RouteList />
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
	<button
		type="button"
		class="absolute top-4 left-4 z-40 rounded-md border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-md transition hover:bg-slate-50 focus:ring-2 focus:ring-slate-400 focus:ring-offset-2 focus:outline-none"
		class:hidden={isSidebarOpen}
		aria-label="Show sidebar"
		onclick={() => (isSidebarOpen = true)}
	>
		Show sidebar
	</button>

	<main class="min-h-0 flex-1">
		{#if activeMapStyle}
			<MapLibre class="h-full w-full" style={activeMapStyle} bind:map />
		{:else}
			<div class="flex h-full w-full items-center justify-center bg-slate-200">
				<p class="text-sm text-slate-600">Loading map...</p>
			</div>
		{/if}
	</main>
</div>
