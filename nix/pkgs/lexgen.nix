{
  buildGoModule,
  indigo,
}:
buildGoModule {
  pname = "lexgen";
  version = "0.1.0";
  src = indigo;
  subPackages = ["cmd/lexgen"];
  vendorHash = "sha256-VbDrcN4r5b7utRFQzVsKgDsVgdQLSXl7oZ5kdPA/huw=";
  doCheck = false;
}
