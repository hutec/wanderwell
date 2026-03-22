<script lang="ts">
	import type { Route } from '$lib/types/route';

	let {
		features,
		onSelect
	}: {
		features: Route[];
		onSelect: (id: number) => void;
	} = $props();

	const formatDate = (dateStr: string) =>
		dateStr
			? new Date(dateStr).toLocaleDateString(undefined, {
					year: 'numeric',
					month: 'long',
					day: 'numeric'
				})
			: 'Unknown date';

	const formatDistance = (distance: number | null) =>
		distance != null ? `${distance.toFixed(1)} km` : 'Unknown distance';
</script>

<div class="popup-container">
	{#each features as feature, i (feature.id)}
		{#if i > 0}<hr />{/if}
		<div
			class="popup-row"
			role="button"
			tabindex="0"
			onclick={() => onSelect(feature.id)}
			onkeydown={(e) => e.key === 'Enter' && onSelect(feature.id)}
		>
			<strong>{feature.name}</strong><br />
			<span class="meta">{formatDate(feature.start_date)} · {formatDistance(feature.distance)}</span
			>
		</div>
	{/each}
</div>

<style>
	.popup-container {
		max-height: 200px;
		overflow: auto;
		padding: 6px;
		box-sizing: border-box;
	}

	hr {
		margin: 6px 0;
	}

	.popup-row {
		margin-bottom: 4px;
		cursor: pointer;
		padding: 4px 6px;
		border-radius: 4px;
		transition: background 0.15s;
	}

	.popup-row:hover {
		background-color: #f1f5f9;
	}

	.meta {
		font-size: 0.85em;
		color: #64748b;
	}
</style>
