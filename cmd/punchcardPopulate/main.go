package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func main() {
	db, err := sql.Open("sqlite3", "./appview.db")
	if err != nil {
		log.Fatal("Failed to open database:", err)
	}
	defer db.Close()

	const did = "did:plc:qfpnj4og54vl56wngdriaxug"

	now := time.Now()
	start := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)

	tx, err := db.Begin()
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := tx.Prepare("INSERT INTO punchcard (did, date, count) VALUES (?, ?, ?)")
	if err != nil {
		log.Fatal(err)
	}
	defer stmt.Close()

	for day := start; !day.After(now); day = day.AddDate(0, 0, 1) {
		count := rand.Intn(16) // 0–5
		dateStr := day.Format("2006-01-02")
		_, err := stmt.Exec(did, dateStr, count)
		if err != nil {
			log.Println("Failed to insert for date %s: %v", dateStr, err)
		}
	}

	if err := tx.Commit(); err != nil {
		log.Fatal("Failed to commit:", err)
	}

	fmt.Println("Done populating punchcard.")
}
