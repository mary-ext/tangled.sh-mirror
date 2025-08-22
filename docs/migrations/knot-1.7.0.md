# Upgrading from v1.7.0

After v1.7.0, knot secrets have been deprecated. You no
longer need a secret from the appview to run a knot. All
authorized commands between services to knots are managed
via [Service
Auth](https://atproto.com/specs/xrpc#inter-service-authentication-jwt).
Knots will be read-only until upgraded.

Upgrading is quite easy, in essence:

- `KNOT_SERVER_SECRET` is no more, you can remove this
  environment variable entirely
- `KNOT_SERVER_OWNER` is now required on boot, set this to
  your DID. You can find your DID in the
  [settings](https://tangled.sh/settings) page.
- Restart your knot once you have replace the environment
  variable
- Head to the [knot dashboard](https://tangled.sh/knots) and
  hit the "retry" button to verify your knot. This simply
  writes a `sh.tangled.knot` record to your PDS.

## Nix

If you use the nix module, simply bump the flake to the
latest revision, and change your config block like so:

```diff
 services.tangled-knot = {
   enable = true;
   server = {
-    secretFile = /path/to/secret;
+    owner = "did:plc:foo";
     .
     .
     .
   };
 };
```
