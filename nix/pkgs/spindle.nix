{
  buildGoApplication,
  modules,
  sqlite-lib,
  gitignoreSource,
}:
buildGoApplication {
  pname = "spindle";
  version = "0.1.0";
  src = gitignoreSource ../..;
  inherit modules;

  doCheck = false;

  subPackages = ["cmd/spindle"];
  tags = ["libsqlite3"];

  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  CGO_ENABLED = 1;
}
