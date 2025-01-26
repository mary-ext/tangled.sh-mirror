use tokio_rusqlite::Connection;

pub struct Db {
    pub conn: Connection,
}

impl Db {
    pub fn new(conn: Connection) -> Self {
        Self { conn }
    }

    pub async fn setup(&self) {
        self.conn
            .call(|c| {
                c.execute(
                    "
                    CREATE TABLE IF NOT EXISTS keys (
                        id INTEGER PRIMARY KEY AUTOINCREMENT,
                        did TEXT NOT NULL,
                        handle TEXT NOT NULL,
                        key TEXT NOT NULL,
                        name TEXT NOT NULL,
                        UNIQUE(did, handle, key)
                    )
                    ",
                    [],
                )?;
                Ok(())
            })
            .await
            .expect("failed to create keys table");
    }
}
