export default {
	async fetch(request, env) {
		const url = new URL(request.url);
		const { pathname } = url;

		if (!pathname || pathname === '/') {
			return new Response(`This is Tangled's avatar service. It fetches your pretty avatar from Bluesky and caches it on Cloudflare.
You can't use this directly unforunately since all requests are signed and may only originate from the appview.`);
		}

		const cache = caches.default;

		let cacheKey = request.url;
		let response = await cache.match(cacheKey);
		if (response) {
			return response;
		}

		const pathParts = pathname.slice(1).split('/');
		if (pathParts.length < 2) {
			return new Response('Bad URL', { status: 400 });
		}

		const [signatureHex, actor] = pathParts;

		const actorBytes = new TextEncoder().encode(actor);

		const key = await crypto.subtle.importKey(
			'raw',
			new TextEncoder().encode(env.AVATAR_SHARED_SECRET),
			{ name: 'HMAC', hash: 'SHA-256' },
			false,
			['sign', 'verify'],
		);

		const computedSigBuffer = await crypto.subtle.sign('HMAC', key, actorBytes);
		const computedSig = Array.from(new Uint8Array(computedSigBuffer))
			.map((b) => b.toString(16).padStart(2, '0'))
			.join('');

		console.log({
			level: 'debug',
			message: 'avatar request for: ' + actor,
			computedSignature: computedSig,
			providedSignature: signatureHex,
		});

		const sigBytes = Uint8Array.from(signatureHex.match(/.{2}/g).map((b) => parseInt(b, 16)));
		const valid = await crypto.subtle.verify('HMAC', key, sigBytes, actorBytes);

		if (!valid) {
			return new Response('Invalid signature', { status: 403 });
		}

		try {
			const profileResponse = await fetch(`https://public.api.bsky.app/xrpc/app.bsky.actor.getProfile?actor=${actor}`, { method: 'GET' });
			const profile = await profileResponse.json();
			const avatar = profile.avatar;

			if (!avatar) {
				return new Response(`avatar not found for ${actor}.`, { status: 404 });
			}

			// fetch the actual avatar image
			const avatarResponse = await fetch(avatar);
			if (!avatarResponse.ok) {
				return new Response(`failed to fetch avatar for ${actor}.`, { status: avatarResponse.status });
			}

			const avatarData = await avatarResponse.arrayBuffer();
			const contentType = avatarResponse.headers.get('content-type') || 'image/jpeg';

			response = new Response(avatarData, {
				headers: {
					'Content-Type': contentType,
					'Cache-Control': 'public, max-age=3600',
				},
			});

			// cache it in cf using request.url as the key
			await cache.put(cacheKey, response.clone());

			return response;
		} catch (error) {
			return new Response(`error fetching avatar: ${error.message}`, { status: 500 });
		}
	},
};
