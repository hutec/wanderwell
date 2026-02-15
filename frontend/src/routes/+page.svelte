<script lang="ts">
	import RouteList from '$lib/RouteList.svelte';
	import { routesState } from '$lib/state.svelte';
	import { getBaseStyle } from '$lib/mapStyle.svelte';
	import { tileServerEndpoint } from '$lib/config';
	import { checkAuth, login, logout, authState } from '$lib/auth.svelte';

	import { MapLibre } from 'svelte-maplibre-gl';
	import type { StyleSpecification } from 'maplibre-gl';

	$effect(() => {
		checkAuth();
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
			style.sources.AllRoutes.url = tileServerEndpoint(`/data/${authState.currentUser?.id}.json`);
		}

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

	let isSidebarOpen = $state(true);
</script>

<div class="relative flex h-screen w-full overflow-hidden bg-slate-100 text-slate-900">
	{#if isSidebarOpen}
		<aside
			class="relative z-30 flex h-full w-80 max-w-[85vw] shrink-0 flex-col border-r border-slate-200 bg-white shadow-sm"
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
					<div class="mb-4 flex">
						<button
							type="button"
							class="self-start rounded-md border border-amber-300 bg-amber-100 px-3 py-1.5 text-sm font-medium text-amber-900 transition hover:bg-amber-200"
							onclick={() => (routesState.routes = [])}
						>
							Reset selection
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
	{:else}
		<button
			type="button"
			class="absolute top-4 left-4 z-40 rounded-md border border-slate-200 bg-white px-3 py-2 text-sm font-medium text-slate-700 shadow-md transition hover:bg-slate-50 focus:ring-2 focus:ring-slate-400 focus:ring-offset-2 focus:outline-none"
			aria-label="Show sidebar"
			onclick={() => (isSidebarOpen = true)}
		>
			Show sidebar
		</button>
	{/if}

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
