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
  env.CGO_ENABLED = 0;
}
