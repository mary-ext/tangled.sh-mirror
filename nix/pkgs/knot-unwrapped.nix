{
  buildGoApplication,
  modules,
  sqlite-lib,
  src,
}:
buildGoApplication {
  pname = "knot";
  version = "0.1.0";
  inherit src modules;

  doCheck = false;

  subPackages = ["cmd/knot"];
  tags = ["libsqlite3"];

  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  CGO_ENABLED = 1;
}
