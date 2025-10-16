run compose
```
docker compose up -d
```

trust the cert (macOS)
```
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./local-infra/cert/localtangled/root.crt
```
