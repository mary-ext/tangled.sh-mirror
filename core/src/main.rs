use std::sync::Arc;

use atrium_api::{
    agent::{AtpAgent, store::MemorySessionStore},
    did_doc::DidDocument,
    types::string::AtIdentifier,
};
use atrium_common::resolver::Resolver;
use atrium_identity::{
    did::{CommonDidResolver, CommonDidResolverConfig, DEFAULT_PLC_DIRECTORY_URL},
    handle::{AtprotoHandleResolver, AtprotoHandleResolverConfig, DnsTxtResolver},
};
use atrium_oauth_client::DefaultHttpClient;
use atrium_xrpc::HttpClient;
use atrium_xrpc_client::isahc::IsahcClient;
use axum::{Router, routing};
use hickory_resolver::TokioAsyncResolver;

#[tokio::main]
async fn main() {
    let session_store = tower_sessions::MemoryStore::default();
    let session_layer = tower_sessions::SessionManagerLayer::new(session_store)
        .with_secure(false)
        .with_expiry(tower_sessions::Expiry::OnSessionEnd);

    let app_state = AppState::new();
    let service = Router::new()
        .route("/", routing::get(index::get))
        // .route("/test-auth", routing::get(test_atproto::get))
        .route("/login", routing::get(login::get).post(login::post))
        // .route("/callback", routing::get(callback::get))
        .layer(session_layer)
        .with_state(app_state);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:3000").await.unwrap();
    axum::serve(listener, service).await.unwrap();
}

#[derive(Clone)]
struct AppState {
    did_resolver: Arc<CommonDidResolver<DefaultHttpClient>>,
    handle_resolver: Arc<AtprotoHandleResolver<HickoryDnsTxtResolver, DefaultHttpClient>>,
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

fn handle_resolver<H: HttpClient>(
    http_client: Arc<H>,
) -> AtprotoHandleResolver<HickoryDnsTxtResolver, H> {
    AtprotoHandleResolver::new(AtprotoHandleResolverConfig {
        dns_txt_resolver: HickoryDnsTxtResolver::default(),
        http_client,
    })
}

mod login {
    use axum::{extract::Form, http::StatusCode, response::IntoResponse};
    use serde::Deserialize;

    use super::*;

    pub async fn get() -> String {
        "hello world".to_owned()
    }

    #[derive(Deserialize, Debug)]
    pub struct Req {
        handle: AtIdentifier,
        app_password: String,
    }

    pub async fn post(session: tower_sessions::Session, Form(req): Form<Req>) -> impl IntoResponse {
        let agent = AtpAgent::new(
            IsahcClient::new("https://dummy.example"),
            MemorySessionStore::default(),
        );
        let res = agent.login(req.handle, req.app_password).await.unwrap();
        println!("logged in as {:?} ({:?})", res.handle, res.did);
        session.insert("at_session", res).await.unwrap();
        println!("stored session");
        StatusCode::OK
    }
}

// dummy endpoint to test if sessions are working
mod index {
    use super::*;

    pub async fn get(session: tower_sessions::Session) -> &'static str {
        match session
            .get::<atrium_api::agent::Session>("at_session")
            .await
            .unwrap()
        {
            None => "no session",
            Some(s) => {
                let agent = AtpAgent::new(
                    IsahcClient::new("https://dummy.example"),
                    MemorySessionStore::default(),
                );
                println!("resuming session of {:?} ({:?})", s.handle, s.did);
                agent.resume_session(s).await.unwrap();
                "resuming session"
            }
        }
    }
}

struct HickoryDnsTxtResolver {
    resolver: TokioAsyncResolver,
}

impl Default for HickoryDnsTxtResolver {
    fn default() -> Self {
        Self {
            resolver: TokioAsyncResolver::tokio_from_system_conf()
                .expect("failed to create resolver"),
        }
    }
}

impl DnsTxtResolver for HickoryDnsTxtResolver {
    async fn resolve(
        &self,
        query: &str,
    ) -> core::result::Result<Vec<String>, Box<dyn std::error::Error + Send + Sync + 'static>> {
        Ok(self
            .resolver
            .txt_lookup(query)
            .await?
            .iter()
            .map(|txt| txt.to_string())
            .collect())
    }
}
