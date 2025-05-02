export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/" || url.pathname === "") {
      return new Response(
        "This is Tangled's Camo service. It proxies images served from knots via Cloudflare.",
      );
    }

    const cache = caches.default;

    const pathParts = url.pathname.slice(1).split("/");
    if (pathParts.length < 2) {
      return new Response("Bad URL", { status: 400 });
    }

    const [signatureHex, ...hexUrlParts] = pathParts;
    const hexUrl = hexUrlParts.join("");
    const urlBytes = Uint8Array.from(
      hexUrl.match(/.{2}/g).map((b) => parseInt(b, 16)),
    );
    const targetUrl = new TextDecoder().decode(urlBytes);

    // check if we have an entry in the cache with the target url
    let cacheKey = new Request(targetUrl);
    let response = await cache.match(cacheKey);
    if (response) {
      return response;
    }

    // else compute the signature
    const key = await crypto.subtle.importKey(
      "raw",
      new TextEncoder().encode(env.CAMO_SHARED_SECRET),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign", "verify"],
    );

    const computedSigBuffer = await crypto.subtle.sign("HMAC", key, urlBytes);
    const computedSig = Array.from(new Uint8Array(computedSigBuffer))
      .map((b) => b.toString(16).padStart(2, "0"))
      .join("");

    console.log({
      level: "debug",
      message: "camo target: " + targetUrl,
      computedSignature: computedSig,
      providedSignature: signatureHex,
      targetUrl: targetUrl,
    });

    const sigBytes = Uint8Array.from(
      signatureHex.match(/.{2}/g).map((b) => parseInt(b, 16)),
    );
    const valid = await crypto.subtle.verify("HMAC", key, sigBytes, urlBytes);

    if (!valid) {
      return new Response("Invalid signature", { status: 403 });
    }

    let parsedUrl;
    try {
      parsedUrl = new URL(targetUrl);
      if (!["https:", "http:"].includes(parsedUrl.protocol)) {
        return new Response("Only HTTP(S) allowed", { status: 400 });
      }
    } catch {
      return new Response("Malformed URL", { status: 400 });
    }

    // fetch from the parsed URL
    const res = await fetch(parsedUrl.toString(), {
      headers: { "User-Agent": "Tangled Camo v0.1.0" },
    });

    const headers = new Headers();
    headers.set(
      "Content-Type",
      res.headers.get("Content-Type") || "application/octet-stream",
    );
    headers.set("Cache-Control", "public, max-age=86400, immutable");

    // serve and cache it with cf
    response = new Response(await res.arrayBuffer(), {
      status: res.status,
      headers,
    });

    await cache.put(cacheKey, response.clone());

    return response;
  },
};
