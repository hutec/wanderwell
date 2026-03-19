<script lang="ts">
	import { routesState } from '$lib/state.svelte';

	const formatDistance = (distance: number) => {
		return `${distance.toFixed(2)} km`;
	};

	$effect(() => {
		const id = routesState.focusedRouteId;
		// Also track sidebar open state so the scroll re-fires when the sidebar becomes visible
		const isOpen = routesState.isSidebarOpen;
		if (id == null || !isOpen) return;
		document.getElementById(`route-${id}`)?.scrollIntoView({ block: 'center' });
	});
</script>

<div class="flex h-full flex-col gap-3">
	{#if routesState.isLoadingRoutes}
		<p class="text-sm text-slate-500">Loading routes...</p>
	{:else if routesState.availableRoutes.length === 0}
		<p class="text-sm text-slate-500">No routes found.</p>
	{:else}
		<ul class="flex-1 overflow-auto rounded-xl border border-slate-200 bg-white shadow-sm">
			{#each routesState.availableRoutes as route (route.id)}
				<li
					id="route-{route.id}"
					class="border-b border-slate-100 last:border-b-0"
					class:border-l-2={route.id === routesState.focusedRouteId}
					class:border-l-amber-400={route.id === routesState.focusedRouteId}
				>
					<label
						class="flex cursor-pointer items-start gap-3 px-4 py-3 transition hover:bg-slate-50"
						class:bg-amber-50={route.id === routesState.focusedRouteId}
					>
						<input
							type="checkbox"
							class="mt-0.5 h-4 w-4 rounded border-slate-300 text-amber-500 focus:ring-amber-400"
							value={route}
							bind:group={routesState.routes}
						/>
						<div class="min-w-0">
							<div class="truncate text-sm font-medium text-slate-900">{route.name}</div>
							<div class="text-xs text-slate-500">
								{new Date(route.start_date).toLocaleDateString()} · {formatDistance(route.distance)}
							</div>
						</div>
					</label>
				</li>
			{/each}
		</ul>
	{/if}
</div>
