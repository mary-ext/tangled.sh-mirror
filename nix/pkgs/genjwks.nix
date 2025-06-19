{
  buildGoModule,
  goModHash,
  gitignoreSource,
}:
buildGoModule {
  pname = "genjwks";
  version = "0.1.0";
  src = gitignoreSource ../..;
  subPackages = ["cmd/genjwks"];
  vendorHash = goModHash;
  doCheck = false;
  env.CGO_ENABLED = 0;
}
