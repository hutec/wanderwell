<script lang="ts">
	import { userState } from '$lib/state.svelte';
	import { apiEndpoint } from '$lib/config';

	interface User {
		id: string | number;
		name: string;
	}

	let users = $state<User[]>([]);
	// let selectedUser: User = $state(null);
	let loading = $state(true);

	// fetch users
	$effect(() => {
		fetch(apiEndpoint('/users'))
			.then((res) => res.json())
			.then((data: unknown) => {
				if (Array.isArray(data)) {
					users = data as User[];
				} else {
					users = [];
				}
				loading = false;
			})
			.catch(() => (loading = false));
	});
</script>

{#if loading}
	<p>Loading users...</p>
{:else}
	<select bind:value={userState.user} class="select">
		{#each users as user (user.id)}
			<option value={user}>{user.name}</option>
		{/each}
	</select>
{/if}
