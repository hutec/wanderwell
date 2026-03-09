<script lang="ts">
	import { login } from '$lib/auth.svelte';
	import stravaBtn from '$lib/assets/btn_strava_connect_with_orange.svg';
</script>

<!--
	Connect with Strava button, per Strava API Brand Guidelines §1.1
	https://developers.strava.com/guidelines/
	Uses the official Strava button asset (2x SVG, displayed at 1x: 237×48px).
-->
<button type="button" onclick={login} class="connect-with-strava" aria-label="Connect with Strava">
	<img src={stravaBtn} alt="Connect with Strava" width="237" height="48" />
</button>

<style>
	.connect-with-strava {
		display: inline-block;
		padding: 0;
		background: none;
		border: none;
		cursor: pointer;
		border-radius: 8px;
		transition: opacity 0.15s ease;
	}

	.connect-with-strava:hover {
		opacity: 0.88;
	}

	.connect-with-strava:focus-visible {
		outline: 3px solid #fc5200;
		outline-offset: 2px;
	}

	.connect-with-strava:active {
		opacity: 0.75;
	}

	.connect-with-strava img {
		display: block;
	}
</style>
