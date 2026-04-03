import type { StyleSpecification } from 'maplibre-gl';
import { type BasemapKey } from '$lib/state.svelte';

const BASEMAP_FILES: Record<BasemapKey, string> = {
	graybeard: '/versatilesgraybeard.json',
	colorful: '/versatilescolorful.json'
};

const baseStyleCache: Partial<Record<BasemapKey, StyleSpecification>> = {};

export async function getBaseStyle(key: BasemapKey = 'graybeard'): Promise<StyleSpecification> {
	if (key in baseStyleCache) {
		return baseStyleCache[key] as StyleSpecification;
	}

	const file = BASEMAP_FILES[key] ?? BASEMAP_FILES.graybeard;
	const response = await fetch(file);
	const style: StyleSpecification = await response.json();
	baseStyleCache[key] = style;
	return style;
}
