{
  src,
  buildGoApplication,
  modules,
}:
buildGoApplication {
  pname = "genjwks";
  version = "0.1.0";
  inherit src modules;
  subPackages = ["cmd/genjwks"];
  doCheck = false;
  CGO_ENABLED = 0;
}
