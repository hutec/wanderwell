<script lang="ts">
	import poweredByStrava from '$lib/assets/api_logo_pwrdBy_strava_stack_orange.svg';
</script>

<!--
	"Powered by Strava" attribution, per Strava API Brand Guidelines §1.2 and §4
	https://developers.strava.com/guidelines/
	Uses the official Strava API logo asset. Must appear separate from the app name/logo.
-->
<a
	href="https://www.strava.com"
	target="_blank"
	rel="noopener noreferrer"
	class="powered-by-strava"
>
	<img src={poweredByStrava} alt="Powered by Strava" width="88" height="30" />
</a>

<style>
	.powered-by-strava {
		display: inline-block;
		transition: opacity 0.15s ease;
	}

	.powered-by-strava:hover {
		opacity: 0.8;
	}

	.powered-by-strava img {
		display: block;
	}
</style>
