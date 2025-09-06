{
  buildGoApplication,
  modules,
  sqlite-lib,
  src,
}:
let
  version = "1.8.1-alpha";
in
buildGoApplication {
  pname = "knot";
  version = "1.8.1";
  inherit src modules;

  doCheck = false;

  subPackages = ["cmd/knot"];
  tags = ["libsqlite3"];

  ldflags = [
    "-X tangled.sh/tangled.sh/core/knotserver/xrpc.version=${version}"
  ];

  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  CGO_ENABLED = 1;
}
