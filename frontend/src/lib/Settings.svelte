<script lang="ts">
	import { routesState, BASEMAP_KEYS, type BasemapKey } from '$lib/state.svelte';

	const PRESET_COLORS = [
		{ name: 'Default Pink', hex: '#cb6e94' },
		{ name: 'Rose', hex: '#e11d48' },
		{ name: 'Orange', hex: '#f97316' },
		{ name: 'Amber', hex: '#f59e0b' },
		{ name: 'Emerald', hex: '#10b981' },
		{ name: 'Cyan', hex: '#06b6d4' },
		{ name: 'Blue', hex: '#2563eb' },
		{ name: 'Purple', hex: '#8b5cf6' }
	];

	const DEFAULT_COLOR = '#cb6e94';
	const DEFAULT_WIDTH = 0.9;

	function resetDefaults() {
		routesState.lineColor = DEFAULT_COLOR;
		routesState.lineWidth = DEFAULT_WIDTH;
	}
</script>

<div class="flex flex-col gap-6 p-1">
	<!-- Basemap section -->
	<div class="flex flex-col gap-2">
		<label
			class="text-xs font-semibold tracking-wider text-slate-500 uppercase"
			for="basemap-group"
		>
			Basemap
		</label>
		<div id="basemap-group" class="flex justify-center">
			<div class="inline-flex w-full rounded-lg border border-slate-200 bg-slate-100 p-0.5">
				{#each BASEMAP_KEYS as key (key)}
					<label
						class="flex-1 cursor-pointer rounded-md py-1.5 text-center text-sm transition-all select-none"
						class:bg-white={routesState.selectedBasemap === key}
						class:shadow-sm={routesState.selectedBasemap === key}
						class:font-medium={routesState.selectedBasemap === key}
						class:text-slate-900={routesState.selectedBasemap === key}
						class:text-slate-500={routesState.selectedBasemap !== key}
					>
						<input
							type="radio"
							name="basemap"
							class="sr-only"
							bind:group={routesState.selectedBasemap}
							value={key as BasemapKey}
						/>
						{key.charAt(0).toUpperCase() + key.slice(1)}
					</label>
				{/each}
			</div>
		</div>
	</div>

	<!-- Line Style section -->
	<div class="flex flex-col gap-4">
		<div class="flex items-center justify-between">
			<span class="text-xs font-semibold tracking-wider text-slate-500 uppercase">
				Line Style
			</span>
			{#if routesState.lineColor !== DEFAULT_COLOR || routesState.lineWidth !== DEFAULT_WIDTH}
				<button
					type="button"
					class="text-xs font-medium text-amber-600 hover:text-amber-700 hover:underline"
					onclick={resetDefaults}
				>
					Reset defaults
				</button>
			{/if}
		</div>

		<!-- Line Color -->
		<div class="flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-3 shadow-sm">
			<label for="line-color-picker" class="text-sm font-medium text-slate-700"> Line Color </label>

			<div class="flex items-center gap-3">
				<input
					id="line-color-picker"
					type="color"
					bind:value={routesState.lineColor}
					class="h-9 w-10 cursor-pointer rounded border border-slate-300 bg-white p-0.5"
				/>
				<input
					type="text"
					bind:value={routesState.lineColor}
					placeholder="#cb6e94"
					class="w-28 rounded-md border border-slate-300 px-2.5 py-1.5 font-mono text-sm text-slate-800 uppercase focus:border-amber-500 focus:ring-1 focus:ring-amber-500 focus:outline-none"
				/>
			</div>

			<!-- Color Presets -->
			<div class="mt-1 flex flex-wrap gap-1.5">
				{#each PRESET_COLORS as preset (preset.hex)}
					<button
						type="button"
						title={preset.name}
						class="h-6 w-6 rounded-full border border-slate-300 transition-transform hover:scale-110 focus:ring-2 focus:ring-amber-400 focus:ring-offset-1 focus:outline-none"
						class:ring-2={routesState.lineColor.toLowerCase() === preset.hex.toLowerCase()}
						class:ring-amber-500={routesState.lineColor.toLowerCase() === preset.hex.toLowerCase()}
						style="background-color: {preset.hex};"
						onclick={() => (routesState.lineColor = preset.hex)}
					></button>
				{/each}
			</div>
		</div>

		<!-- Line Width -->
		<div class="flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-3 shadow-sm">
			<div class="flex items-center justify-between">
				<label for="line-width-slider" class="text-sm font-medium text-slate-700">
					Line Width
				</label>
				<span
					class="rounded bg-slate-100 px-2 py-0.5 font-mono text-xs font-semibold text-slate-700"
				>
					{routesState.lineWidth} px
				</span>
			</div>

			<input
				id="line-width-slider"
				type="range"
				min="0.5"
				max="10"
				step="0.1"
				bind:value={routesState.lineWidth}
				class="w-full cursor-pointer accent-amber-500"
			/>

			<div class="flex justify-between text-[10px] text-slate-400">
				<span>0.5 px</span>
				<span>5.0 px</span>
				<span>10.0 px</span>
			</div>
		</div>
	</div>
</div>
