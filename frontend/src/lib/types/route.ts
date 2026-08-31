export type Route = {
	id: number;
	name: string;
	distance: number; // in kilometers
	elevation: number; // in meters
	start_date: string; // ISO date string
	bounds: string; // "minLat,minLng,maxLat,maxLng"
};
