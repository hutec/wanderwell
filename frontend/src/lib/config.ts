import { PUBLIC_API_URL, PUBLIC_TILE_SERVER_URL } from '$env/static/public';

export const config = {
	apiUrl: PUBLIC_API_URL,
	tileServerUrl: PUBLIC_TILE_SERVER_URL
} as const;

export function apiEndpoint(path: string): string {
	return `${config.apiUrl}${path}`;
}
export function tileServerEndpoint(path: string): string {
	return `${config.tileServerUrl}${path}`;
}
