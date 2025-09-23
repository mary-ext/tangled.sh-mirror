package db

import (
	"tangled.org/core/appview/models"
	"time"
)

func AddPublicKey(e Execer, did, name, key, rkey string) error {
	_, err := e.Exec(
		`insert or ignore into public_keys (did, name, key, rkey)
		 values (?, ?, ?, ?)`,
		did, name, key, rkey)
	return err
}

func DeletePublicKey(e Execer, did, name, key string) error {
	_, err := e.Exec(`
		delete from public_keys
		where did = ? and name = ? and key = ?`,
		did, name, key)
	return err
}

func DeletePublicKeyByRkey(e Execer, did, rkey string) error {
	_, err := e.Exec(`
		delete from public_keys
		where did = ? and rkey = ?`,
		did, rkey)
	return err
}

func GetAllPublicKeys(e Execer) ([]models.PublicKey, error) {
	var keys []models.PublicKey

	rows, err := e.Query(`select key, name, did, rkey, created from public_keys`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey models.PublicKey
		var createdAt string
		if err := rows.Scan(&publicKey.Key, &publicKey.Name, &publicKey.Did, &publicKey.Rkey, &createdAt); err != nil {
			return nil, err
		}
		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
		publicKey.Created = &createdAtTime
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}

func GetPublicKeysForDid(e Execer, did string) ([]models.PublicKey, error) {
	var keys []models.PublicKey

	rows, err := e.Query(`select did, key, name, rkey, created from public_keys where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var publicKey models.PublicKey
		var createdAt string
		if err := rows.Scan(&publicKey.Did, &publicKey.Key, &publicKey.Name, &publicKey.Rkey, &createdAt); err != nil {
			return nil, err
		}
		createdAtTime, _ := time.Parse(time.RFC3339, createdAt)
		publicKey.Created = &createdAtTime
		keys = append(keys, publicKey)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return keys, nil
}
