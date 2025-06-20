{
  buildGoModule,
  stdenv,
  htmx-src,
  htmx-ws-src,
  lucide-src,
  inter-fonts-src,
  ibm-plex-mono-src,
  tailwindcss,
  sqlite-lib,
  goModHash,
  gitignoreSource,
  uglify-js,
}:
buildGoModule {
  inherit stdenv;

  pname = "appview";
  version = "0.1.0";
  src = gitignoreSource ../..;

  postUnpack = ''
    pushd source
    mkdir -p appview/pages/static/{fonts,icons}
    ${uglify-js}/bin/uglifyjs ${htmx-src} ${htmx-ws-src} -c -m > appview/pages/static/htmx.min.js
    cp -rf ${lucide-src}/*.svg appview/pages/static/icons/
    cp -f ${inter-fonts-src}/web/InterVariable*.woff2 appview/pages/static/fonts/
    cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 appview/pages/static/fonts/
    cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono-Regular.woff2 appview/pages/static/fonts/
    ${tailwindcss}/bin/tailwindcss -i input.css -o appview/pages/static/tw.css
    popd
  '';

  doCheck = false;
  subPackages = ["cmd/appview"];
  vendorHash = goModHash;

  tags = "libsqlite3";
  env.CGO_CFLAGS = "-I ${sqlite-lib}/include ";
  env.CGO_LDFLAGS = "-L ${sqlite-lib}/lib";
  env.CGO_ENABLED = 1;
}
