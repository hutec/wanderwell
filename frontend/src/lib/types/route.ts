export type Route = {
	id: number;
	name: string;
	distance: number; // in kilometers
	start_date: string; // ISO date string
	bounds: string; // "minLat,minLng,maxLat,maxLng"
};
