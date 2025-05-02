#!/usr/bin/env bash

IMG_URL="https://catoftheday.com/archive/2024/November/17.jpg"
SECRET="f3e27203049b6cb685670c9d8393783e57a0db33f90494c0ccb303721e36137e"

HEX_URL=$(echo -n "$IMG_URL" | xxd -p | tr -d '\n')
SIG=$(echo -n "$IMG_URL" | openssl dgst -sha256 -hmac "$SECRET" -binary | xxd -p | tr -d '\n')


echo "Signed URL:"
echo "https://camo.anirudh-s-account.workers.dev/$SIG/$HEX_URL"
