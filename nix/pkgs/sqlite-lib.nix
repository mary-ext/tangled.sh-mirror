{
  gcc,
  stdenv,
  sqlite-lib-src,
}:
stdenv.mkDerivation {
  name = "sqlite-lib";
  src = sqlite-lib-src;
  nativeBuildInputs = [gcc];
  buildPhase = ''
    gcc -c sqlite3.c
    ar rcs libsqlite3.a sqlite3.o
    ranlib libsqlite3.a
    mkdir -p $out/include $out/lib
    cp *.h $out/include
    cp libsqlite3.a $out/lib
  '';
}
