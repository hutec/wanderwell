<script lang="ts">
	import { routesState } from '$lib/state.svelte';
	import { apiEndpoint } from '$lib/config';

	let { userID } = $props();

	let routes = $state([]);
	let loading = $state(true);

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
					routes = data as any[];
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

<div class="flex h-full flex-col gap-y-3">
	{#if loading}
		<p>Loading routes...</p>
	{:else if routes.length === 0}
		<p>No routes found.</p>
	{:else}
		<button class="btn bg-base-100 top-0 z-10 rounded" onclick={() => (routesState.routes = [])}
			>Reset Selection</button
		>
		<ul class="list rounded-box bg-base-100 flex-1 overflow-auto shadow-md">
			{#each routes as route (route.id)}
				<li class="list-row py-2">
					<div class="self-center">
						<input
							type="checkbox"
							class="checkbox"
							value={route.id}
							bind:group={routesState.routes}
						/>
					</div>
					<div>
						<div>{route.name}</div>
						<div class="text-xs font-semibold opacity-60">
							{new Date(route.start_date).toLocaleDateString()}
						</div>
					</div>
				</li>
			{/each}
		</ul>
	{/if}
</div>
