{
  buildGoModule,
  indigo,
}:
buildGoModule {
  pname = "lexgen";
  version = "0.1.0";
  src = indigo;
  subPackages = ["cmd/lexgen"];
  vendorHash = "sha256-pGc29fgJFq8LP7n/pY1cv6ExZl88PAeFqIbFEhB3xXs=";
  doCheck = false;
}
