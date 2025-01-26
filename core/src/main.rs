use std::sync::Arc;
use tokio::sync::Mutex;

use atrium_api::{
    agent::{AtpAgent, store::MemorySessionStore},
    client::AtpServiceClient,
    did_doc::DidDocument,
    types::string::AtIdentifier,
    xrpc::types::AuthorizationToken,
};
use atrium_common::resolver::Resolver;
use atrium_identity::{
    did::{CommonDidResolver, CommonDidResolverConfig, DEFAULT_PLC_DIRECTORY_URL},
    handle::{AtprotoHandleResolver, AtprotoHandleResolverConfig, DnsTxtResolver},
};
use atrium_oauth_client::{
    AtprotoClientMetadata, AtprotoLocalhostClientMetadata, AuthorizeOptionPrompt, AuthorizeOptions,
    DefaultHttpClient, DpopClient, KnownScope, OAuthClient, OAuthClientConfig, OAuthResolverConfig,
    Scope, store::state::MemoryStateStore,
};
use atrium_xrpc::HttpClient;
use atrium_xrpc_client::isahc::{IsahcClient, IsahcClientBuilder};
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
    inner: Arc<Mutex<AppStateInner>>,
    did_resolver: Arc<CommonDidResolver<DefaultHttpClient>>,
    handle_resolver: Arc<AtprotoHandleResolver<HickoryDnsTxtResolver, DefaultHttpClient>>,
}

impl AppState {
    fn new() -> Self {
        let client = Arc::new(DefaultHttpClient::default());
        Self {
            inner: Arc::new(Mutex::new(AppStateInner::new(client.clone()))),
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

struct AppStateInner {
    agent: Option<AtpAgent<MemorySessionStore, IsahcClient>>,
}

impl AppStateInner {
    fn new(http_client: Arc<DefaultHttpClient>) -> Self {
        // let config = OAuthClientConfig {
        //     client_metadata: AtprotoLocalhostClientMetadata {
        //         // TODO: change this
        //         redirect_uris: Some(vec![String::from("http://127.0.0.1:3000/callback")]),
        //         scopes: Some(vec![
        //             Scope::Known(KnownScope::Atproto),
        //             Scope::Known(KnownScope::TransitionGeneric),
        //         ]),
        //     },
        //     keys: None,
        //     resolver: OAuthResolverConfig {
        //         did_resolver: did_resolver(http_client.clone()),
        //         handle_resolver: handle_resolver(http_client.clone()),
        //         authorization_server_metadata: Default::default(),
        //         protected_resource_metadata: Default::default(),
        //     },
        //     state_store: MemoryStateStore::default(),
        // };
        // let oauth_client = OAuthClient::new(config).unwrap();
        Self { agent: None }
    }
}

mod login {
    use axum::{
        extract::{Form, State},
        http::StatusCode,
        response::IntoResponse,
    };
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

    pub async fn post(
        State(state): State<AppState>,
        session: tower_sessions::Session,
        Form(req): Form<Req>,
    ) -> impl IntoResponse {
        let did_document = state.resolve_did_document(&req.handle).await.unwrap();
        let agent = AtpAgent::new(
            IsahcClient::new(did_document.get_pds_endpoint().unwrap()),
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
                // let did_doc = s.did_doc.unwrap();
                let agent = AtpAgent::new(
                    IsahcClient::new("https://bsky.social"),
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
