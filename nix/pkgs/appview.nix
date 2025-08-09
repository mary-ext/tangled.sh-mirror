{
  buildGoApplication,
  modules,
  appview-static-files,
  sqlite-lib,
  src,
}:
buildGoApplication {
  pname = "appview";
  version = "0.1.0";
  inherit src modules;

  postUnpack = ''
    pushd source
    mkdir -p appview/pages/static
    cp -frv ${appview-static-files}/* appview/pages/static
    popd
  '';

  doCheck = false;
  subPackages = ["cmd/appview"];

  tags = ["libsqlite3"];
  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  CGO_ENABLED = 1;
}
