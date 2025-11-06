# Migrations

This document is laid out in reverse-chronological order.
Newer migration guides are listed first, and older guides
are further down the page.

## Upgrading from v1.8.x

After v1.8.2, the HTTP API for knot and spindles have been
deprecated and replaced with XRPC. Repositories on outdated
knots will not be viewable from the appview. Upgrading is
straightforward however.

For knots:

- Upgrade to latest tag (v1.9.0 or above)
- Head to the [knot dashboard](https://tangled.org/knots) and
  hit the "retry" button to verify your knot

For spindles:

- Upgrade to latest tag (v1.9.0 or above)
- Head to the [spindle
  dashboard](https://tangled.org/spindles) and hit the
  "retry" button to verify your spindle

## Upgrading from v1.7.x

After v1.7.0, knot secrets have been deprecated. You no
longer need a secret from the appview to run a knot. All
authorized commands to knots are managed via [Inter-Service
Authentication](https://atproto.com/specs/xrpc#inter-service-authentication-jwt).
Knots will be read-only until upgraded.

Upgrading is quite easy, in essence:

- `KNOT_SERVER_SECRET` is no more, you can remove this
  environment variable entirely
- `KNOT_SERVER_OWNER` is now required on boot, set this to
  your DID. You can find your DID in the
  [settings](https://tangled.org/settings) page.
- Restart your knot once you have replaced the environment
  variable
- Head to the [knot dashboard](https://tangled.org/knots) and
  hit the "retry" button to verify your knot. This simply
  writes a `sh.tangled.knot` record to your PDS.

If you use the nix module, simply bump the flake to the
latest revision, and change your config block like so:

```diff
 services.tangled.knot = {
   enable = true;
   server = {
-    secretFile = /path/to/secret;
+    owner = "did:plc:foo";
   };
 };
```
