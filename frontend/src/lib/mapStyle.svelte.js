/** @type {any} */
let baseStyleCache = null;

/**
 * Fetches the base map style from the static directory
 * @returns {Promise<any>} The base map style object
 */
export async function getBaseStyle() {
	if (baseStyleCache) {
		return baseStyleCache;
	}

	const response = await fetch('/versatilesgraybeard.json');
	baseStyleCache = await response.json();
	return baseStyleCache;
}
