{
  buildGoModule,
  indigo,
}:
buildGoModule {
  pname = "goat";
  version = "0.1.0";
  src = indigo;
  subPackages = ["cmd/goat"];
  vendorHash = "sha256-VbDrcN4r5b7utRFQzVsKgDsVgdQLSXl7oZ5kdPA/huw=";
  doCheck = false;
}
