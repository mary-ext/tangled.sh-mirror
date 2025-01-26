use std::sync::Arc;

use atrium_api::{
    client::AtpServiceClient, did_doc::DidDocument, types::string::AtIdentifier,
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
        .route("/test-auth", routing::get(test_atproto::get))
        .route("/login", routing::get(login::get).post(login::post))
        .route("/callback", routing::get(callback::get))
        .layer(session_layer)
        .with_state(app_state);

    let listener = tokio::net::TcpListener::bind("0.0.0.0:3000").await.unwrap();
    axum::serve(listener, service).await.unwrap();
}

#[derive(Clone)]
struct AppState {
    inner: Arc<AppStateInner>,
}

impl AppState {
    fn new() -> Self {
        Self {
            inner: Arc::new(AppStateInner::new()),
        }
    }
}

struct AppStateInner {
    did_resolver: CommonDidResolver<DefaultHttpClient>,
    handle_resolver: AtprotoHandleResolver<HickoryDnsTxtResolver, DefaultHttpClient>,
    oauth_client: OAuthClient<
        atrium_oauth_client::store::state::MemoryStateStore,
        CommonDidResolver<DefaultHttpClient>,
        atrium_identity::handle::AtprotoHandleResolver<HickoryDnsTxtResolver, DefaultHttpClient>,
    >,
}

impl AppStateInner {
    fn new() -> Self {
        let client = Arc::new(DefaultHttpClient::default());
        let did_resolver = Self::did_resolver(client.clone());
        let handle_resolver = Self::handle_resolver(client.clone());
        let config = OAuthClientConfig {
            client_metadata: AtprotoLocalhostClientMetadata {
                // TODO: change this
                redirect_uris: Some(vec![String::from("http://127.0.0.1:3000/callback")]),
                scopes: Some(vec![
                    Scope::Known(KnownScope::Atproto),
                    Scope::Known(KnownScope::TransitionGeneric),
                ]),
            },
            keys: None,
            resolver: OAuthResolverConfig {
                did_resolver: Self::did_resolver(client.clone()),
                handle_resolver: Self::handle_resolver(client.clone()),
                authorization_server_metadata: Default::default(),
                protected_resource_metadata: Default::default(),
            },
            state_store: MemoryStateStore::default(),
        };
        let oauth_client = OAuthClient::new(config).unwrap();
        Self {
            oauth_client,
            did_resolver,
            handle_resolver,
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

mod login {
    use axum::{
        extract::{Form, State},
        response::{IntoResponse, Redirect, Result},
    };
    use serde::Deserialize;

    use super::*;

    pub async fn get() -> String {
        "hello world".to_owned()
    }

    #[derive(Deserialize, Debug)]
    pub struct Req {
        handle: AtIdentifier,
    }

    pub async fn post(
        State(state): State<AppState>,
        Form(req): Form<Req>,
    ) -> Result<impl IntoResponse> {
        let did_document = state.inner.resolve_did_document(&req.handle).await.unwrap();
        dbg!(&did_document);
        let res = state
            .inner
            .oauth_client
            .authorize(did_document.get_pds_endpoint().unwrap(), AuthorizeOptions {
                scopes: vec![
                    Scope::Known(KnownScope::Atproto),
                    Scope::Known(KnownScope::TransitionGeneric),
                ],
                prompt: Some(AuthorizeOptionPrompt::Login),
                ..Default::default()
            })
            .await
            .unwrap();
        Ok(Redirect::to(&res))
    }
}

mod callback {
    use axum::{
        extract::{Query, State},
        http::StatusCode,
    };

    use super::*;

    pub async fn get(
        Query(params): Query<atrium_oauth_client::CallbackParams>,
        State(state): State<AppState>,
        session: tower_sessions::Session,
    ) -> StatusCode {
        let ts = state.inner.oauth_client.callback(params).await.unwrap();
        let _ = session.insert("bild", &ts).await;
        StatusCode::OK
    }
}

// dummy endpoint to test if sessions are working
mod index {
    use axum::http::StatusCode;

    pub async fn get(session: tower_sessions::Session) -> StatusCode {
        dbg!(
            session
                .get::<atrium_oauth_client::TokenSet>("bild")
                .await
                .unwrap()
        );
        StatusCode::OK
    }
}

struct AuthenticatedClient {
    token: String,
    base_uri: String,
    inner: IsahcClient,
}

impl atrium_xrpc::HttpClient for AuthenticatedClient {
    async fn send_http(
        &self,
        request: atrium_xrpc::http::Request<Vec<u8>>,
    ) -> Result<
        atrium_xrpc::http::Response<Vec<u8>>,
        Box<dyn std::error::Error + Send + Sync + 'static>,
    > {
        self.inner.send_http(request).await
    }
}

impl atrium_xrpc::XrpcClient for AuthenticatedClient {
    fn base_uri(&self) -> String {
        self.base_uri.clone()
    }
    async fn authorization_token(&self, _: bool) -> Option<AuthorizationToken> {
        Some(AuthorizationToken::Dpop(self.token.clone()))
    }
}

// dummy endpoint to perform an atproto request with access token
mod test_atproto {
    use atrium_api::{
        agent::{AtpAgent, store::MemorySessionStore},
        app, com,
    };
    use axum::http::StatusCode;

    use super::*;

    fn get_session_client(
        tokenset: &atrium_oauth_client::TokenSet,
    ) -> AtpServiceClient<AuthenticatedClient> {
        //) -> AtpAgent<MemorySessionStore, AuthenticatedClient> {
        // AtpAgent::new(
        //     AuthenticatedClient {
        //         token: tokenset.access_token.clone(),
        //         base_uri: tokenset.iss.clone(),
        //         inner: IsahcClient::new(tokenset.iss.clone()),
        //     },
        //     MemorySessionStore::default(),
        // )
        AtpServiceClient::new(AuthenticatedClient {
            token: tokenset.access_token.clone(),
            base_uri: tokenset.aud.clone(),
            inner: IsahcClient::new(tokenset.aud.clone()),
        })
    }

    pub async fn get(session: tower_sessions::Session) -> StatusCode {
        let token_set = session
            .get::<atrium_oauth_client::TokenSet>("bild")
            .await
            .ok()
            .flatten()
            .unwrap();
        let client = get_session_client(&token_set);
        let res = client
            .service
            .com
            .atproto
            .repo
            .upload_blob(vec![0])
            .await
            .unwrap();
        dbg!(res);
        StatusCode::OK
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
