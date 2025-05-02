# camo

Camo is Tangled's "camouflage" service much like that of [GitHub's](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-anonymized-urls).

Camo uses a shared secret `CAMO_SHARED_SECRET` to verify HMAC signatures. URLs are of the form:

```
https://camo.tangled.sh/<signature>/<hex-encoded-origin-url>
```

It's pretty barebones for the moment and doesn't support a whole lot of what the
big G's does. Ours is a Cloudflare Worker, deployed using `wrangler` like so:

```
npx wrangler deploy
npx wrangler secrets put CAMO_SHARED_SECRET
```
