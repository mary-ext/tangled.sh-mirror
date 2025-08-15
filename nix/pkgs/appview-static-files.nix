{
  runCommandLocal,
  htmx-src,
  htmx-ws-src,
  lucide-src,
  inter-fonts-src,
  ibm-plex-mono-src,
  sqlite-lib,
  tailwindcss,
  src,
}:
runCommandLocal "appview-static-files" {
  # TOOD(winter): figure out why this is even required after
  # changing the libraries that the tailwindcss binary loads
  sandboxProfile = ''
    (allow file-read* (subpath "/System/Library/OpenSSL"))
  '';
} ''
  mkdir -p $out/{fonts,icons} && cd $out
  cp -f ${htmx-src} htmx.min.js
  cp -f ${htmx-ws-src} htmx-ext-ws.min.js
  cp -rf ${lucide-src}/*.svg icons/
  cp -f ${inter-fonts-src}/web/InterVariable*.woff2 fonts/
  cp -f ${inter-fonts-src}/web/InterDisplay*.woff2 fonts/
  cp -f ${ibm-plex-mono-src}/fonts/complete/woff2/IBMPlexMono*.woff2 fonts/
  # tailwindcss -c $src/tailwind.config.js -i $src/input.css -o tw.css won't work
  # for whatever reason (produces broken css), so we are doing this instead
  cd ${src} && ${tailwindcss}/bin/tailwindcss -i input.css -o $out/tw.css
''
