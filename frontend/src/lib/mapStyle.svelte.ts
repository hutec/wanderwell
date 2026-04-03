import { type BasemapKey } from '$lib/state.svelte';

const BASEMAP_FILES: Record<BasemapKey, string> = {
	graybeard: '/versatilesgraybeard.json',
	colorful: '/versatilescolorful.json'
};

const baseStyleCache: Record<BasemapKey, string> = {};

export async function getBaseStyle(key: BasemapKey = 'graybeard'): Promise<string> {
	if (key in baseStyleCache) {
		return baseStyleCache[key];
	}

	const file = BASEMAP_FILES[key] ?? BASEMAP_FILES.graybeard;
	const response = await fetch(file);
	const style = await response.json();
	baseStyleCache[key] = style;
	return style;
}
