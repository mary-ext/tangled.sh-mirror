use std::sync::Arc;

use atrium_api::{did_doc::DidDocument, types::string::AtIdentifier};
use atrium_common::resolver::Resolver;
use atrium_identity::{
    did::{CommonDidResolver, CommonDidResolverConfig, DEFAULT_PLC_DIRECTORY_URL},
    handle::{AtprotoHandleResolver, AtprotoHandleResolverConfig, DnsTxtResolver},
};
use atrium_oauth_client::{
    AtprotoLocalhostClientMetadata, AuthorizeOptions, DefaultHttpClient, KnownScope, OAuthClient,
    OAuthClientConfig, OAuthResolverConfig, Scope, store::state::MemoryStateStore,
};
use atrium_xrpc::HttpClient;
use axum::{Router, routing};
use hickory_resolver::TokioAsyncResolver;

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

#[tokio::main]
async fn main() {
    let state = AppState::new();
    let service = Router::new()
        .route("/login", routing::get(login::get).post(login::post))
        .route("/callback", routing::get(callback::get))
        .with_state(state);

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

type DidResolver = CommonDidResolver<DefaultHttpClient>;
type HandleResolver = AtprotoHandleResolver<HickoryDnsTxtResolver, DefaultHttpClient>;

struct AppStateInner {
    did_resolver: DidResolver,
    handle_resolver: HandleResolver,
    oauth_client: OAuthClient<MemoryStateStore, DidResolver, HandleResolver>,
}

impl AppStateInner {
    fn new() -> Self {
        let client = Arc::new(DefaultHttpClient::default());
        let did_resolver = Self::did_resolver(client.clone());
        let handle_resolver = Self::handle_resolver(client.clone());
        let config = OAuthClientConfig {
            client_metadata: AtprotoLocalhostClientMetadata {
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
        extract::{Json, State},
        response::{IntoResponse, Redirect, Result},
    };

    use super::*;

    pub async fn get() -> String {
        "hello world".to_owned()
    }

    pub async fn post(
        State(state): State<AppState>,
        Json(handle): Json<AtIdentifier>,
    ) -> Result<impl IntoResponse> {
        let did_document = state.inner.resolve_did_document(&handle).await.unwrap();
        let res = state
            .inner
            .oauth_client
            .authorize(did_document.get_pds_endpoint().unwrap(), AuthorizeOptions {
                scopes: vec![
                    Scope::Known(KnownScope::Atproto),
                    Scope::Known(KnownScope::TransitionGeneric),
                ],
                ..Default::default()
            })
            .await
            .unwrap();
        Ok(Redirect::temporary(&res))
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
    ) -> StatusCode {
        let ts = state.inner.oauth_client.callback(params).await;
        println!("ts: {ts:?}");
        StatusCode::OK
    }
}
