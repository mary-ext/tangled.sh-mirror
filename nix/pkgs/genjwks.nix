{
  buildGoApplication,
  modules,
}:
buildGoApplication {
  pname = "genjwks";
  version = "0.1.0";
  src = ../../cmd/genjwks;
  postPatch = ''
    ln -s ${../../go.mod} ./go.mod
  '';
  postInstall = ''
    mv $out/bin/core $out/bin/genjwks
  '';
  inherit modules;
  doCheck = false;
  CGO_ENABLED = 0;
}
