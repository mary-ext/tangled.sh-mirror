use std::sync::Arc;

use atrium_api::{did_doc::DidDocument, types::string::AtIdentifier};
use atrium_common::resolver::Resolver;
use atrium_identity::{
    did::{CommonDidResolver, CommonDidResolverConfig, DEFAULT_PLC_DIRECTORY_URL},
    handle::{AtprotoHandleResolver, AtprotoHandleResolverConfig},
};
use atrium_oauth_client::DefaultHttpClient;
use atrium_xrpc::HttpClient;
use axum::{Extension, Router, routing};
use tokio_rusqlite::Connection;

mod db;
mod dns;
mod routes;

use db::Db;
use routes::{index, keys, login};

#[tokio::main]
async fn main() {
    let session_store = tower_sessions::MemoryStore::default();
    let session_layer = tower_sessions::SessionManagerLayer::new(session_store)
        .with_secure(false)
        .with_expiry(tower_sessions::Expiry::OnSessionEnd);
    let db = Db::new(Connection::open("bild.db").await.unwrap());
    db.setup().await;

    let app_state = AppState::new();
    let service = Router::new()
        .route("/", routing::get(index::get))
        .route("/login", routing::get(login::get).post(login::post))
        .route("/keys", routing::get(keys::get).put(keys::put))
        .layer(session_layer)
        .with_state(app_state)
        .layer(Extension(Arc::new(db)));

    let listener = tokio::net::TcpListener::bind("0.0.0.0:3000").await.unwrap();
    axum::serve(listener, service).await.unwrap();
}

#[derive(Clone)]
struct AppState {
    did_resolver: Arc<CommonDidResolver<DefaultHttpClient>>,
    handle_resolver: Arc<AtprotoHandleResolver<dns::Resolver, DefaultHttpClient>>,
}

impl AppState {
    fn new() -> Self {
        let client = Arc::new(DefaultHttpClient::default());
        Self {
            did_resolver: Arc::new(did_resolver(client.clone())),
            handle_resolver: Arc::new(handle_resolver(client.clone())),
        }
    }

    async fn resolve_did_document(
        &self,
        ident: &AtIdentifier,
    ) -> Result<DidDocument, atrium_identity::Error> {
        match ident {
            AtIdentifier::Did(did) => Ok(self.did_resolver.resolve(&did).await?),
            AtIdentifier::Handle(handle) => {
                let did = self.handle_resolver.resolve(&handle).await?;
                Ok(self.did_resolver.resolve(&did).await?)
            }
        }
    }
}

fn did_resolver<H: HttpClient>(http_client: Arc<H>) -> CommonDidResolver<H> {
    CommonDidResolver::new(CommonDidResolverConfig {
        plc_directory_url: DEFAULT_PLC_DIRECTORY_URL.to_string(),
        http_client,
    })
}

fn handle_resolver<H: HttpClient>(http_client: Arc<H>) -> AtprotoHandleResolver<dns::Resolver, H> {
    AtprotoHandleResolver::new(AtprotoHandleResolverConfig {
        dns_txt_resolver: dns::Resolver::default(),
        http_client,
    })
}
