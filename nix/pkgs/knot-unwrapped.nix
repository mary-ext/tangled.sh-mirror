{
  buildGoApplication,
  modules,
  sqlite-lib,
  src,
}: let
  version = "1.11.0-alpha";
in
  buildGoApplication {
    pname = "knot";
    inherit src version modules;

    doCheck = false;

    subPackages = ["cmd/knot"];
    tags = ["libsqlite3"];

    ldflags = [
      "-X tangled.org/core/knotserver/xrpc.version=${version}"
    ];

    env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
    env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
    CGO_ENABLED = 1;
  }
