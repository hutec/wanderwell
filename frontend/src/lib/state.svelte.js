/**
 * @typedef {Object} User
 * @property {string | number} id
 * @property {string} name
 */

export const userState = $state({
	/** @type {User | null} */
	user: null
});

export const routesState = $state({
	routes: []
});
