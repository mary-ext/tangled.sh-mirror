{
  buildGoApplication,
  modules,
  htmx-src,
  htmx-ws-src,
  lucide-src,
  inter-fonts-src,
  ibm-plex-mono-src,
  tailwindcss,
  sqlite-lib,
  src,
}:
buildGoApplication {
  pname = "appview";
  version = "0.1.0";
  inherit src modules;

  postUnpack = ''
    pushd source
    mkdir -p appview/pages/static/{fonts,icons}
    cp -f ${htmx-src} appview/pages/static/htmx.min.js
    cp -f ${htmx-ws-src} appview/pages/static/htmx-ext-ws.min.js
    cp -rf ${lucide-src}/*.svg appview/pages/static/icons/
    cp -f ${inter-fonts-src}/web/InterVariable*.woff2 appview/pages/static/fonts/
    cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 appview/pages/static/fonts/
    cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono-Regular.woff2 appview/pages/static/fonts/
    ${tailwindcss}/bin/tailwindcss -i input.css -o appview/pages/static/tw.css
    popd
  '';

  doCheck = false;
  subPackages = ["cmd/appview"];

  tags = ["libsqlite3"];
  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  CGO_ENABLED = 1;
}
