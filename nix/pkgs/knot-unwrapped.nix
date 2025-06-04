{
  buildGoModule,
  stdenv,
  sqlite-lib,
  goModHash,
  gitignoreSource,
}:
buildGoModule {
  pname = "knot";
  version = "0.1.0";
  src = gitignoreSource ../..;

  doCheck = false;

  subPackages = ["cmd/knot"];
  vendorHash = goModHash;
  tags = "libsqlite3";

  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  env.CGO_ENABLED = 1;
}
