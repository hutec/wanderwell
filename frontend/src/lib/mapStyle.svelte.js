/** @type {Map<string, any>} */
const styleCache = new Map();

const BASEMAP_FILES = {
	graybeard: '/versatilesgraybeard.json',
	colorful: '/versatilescolorful.json',
	neutrino: '/versatilesneutrino.json'
};

/**
 * Fetches the base map style for the given key, caching per basemap.
 * @param {'graybeard' | 'colorful' | 'neutrino'} key
 * @returns {Promise<any>} The base map style object
 */
export async function getBaseStyle(key = 'graybeard') {
	if (styleCache.has(key)) {
		return styleCache.get(key);
	}

	const file = BASEMAP_FILES[key] ?? BASEMAP_FILES.graybeard;
	const response = await fetch(file);
	const style = await response.json();
	styleCache.set(key, style);
	return style;
}
