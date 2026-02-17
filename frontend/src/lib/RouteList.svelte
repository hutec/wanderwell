<script lang="ts">
	import { routesState } from '$lib/state.svelte';
	import { apiEndpoint } from '$lib/config';
	import type { Route } from '$lib/types/route';

	let { userID } = $props();

	let routes = $state<Route[]>([]);
	let loading = $state(true);

	const formatDistance = (distance: number) => {
		return `${distance.toFixed(2)} km`;
	};

	// fetch routes for given userID
	$effect(() => {
		if (!userID) {
			routes = [];
			loading = false;
			return;
		}

		loading = true;
		fetch(apiEndpoint(`/route_details`), { credentials: 'include' })
			.then((res) => res.json())
			.then((data: unknown) => {
				if (Array.isArray(data)) {
					routes = data;
				} else {
					routes = [];
				}
				loading = false;
			})
			.catch(() => {
				routes = [];
				loading = false;
			});
	});
</script>

<div class="flex h-full flex-col gap-3">
	{#if loading}
		<p class="text-sm text-slate-500">Loading routes...</p>
	{:else if routes.length === 0}
		<p class="text-sm text-slate-500">No routes found.</p>
	{:else}
		<ul class="flex-1 overflow-auto rounded-xl border border-slate-200 bg-white shadow-sm">
			{#each routes as route (route.id)}
				<li class="border-b border-slate-100 last:border-b-0">
					<label
						class="flex cursor-pointer items-start gap-3 px-4 py-3 transition hover:bg-slate-50"
					>
						<input
							type="checkbox"
							class="mt-0.5 h-4 w-4 rounded border-slate-300 text-amber-500 focus:ring-amber-400"
							value={route.id}
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
