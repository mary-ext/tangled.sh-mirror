package db

import (
	"log"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

func AddReaction(e Execer, reactedByDid string, threadAt syntax.ATURI, kind models.ReactionKind, rkey string) error {
	query := `insert or ignore into reactions (reacted_by_did, thread_at, kind, rkey) values (?, ?, ?, ?)`
	_, err := e.Exec(query, reactedByDid, threadAt, kind, rkey)
	return err
}

// Get a reaction record
func GetReaction(e Execer, reactedByDid string, threadAt syntax.ATURI, kind models.ReactionKind) (*models.Reaction, error) {
	query := `
	select reacted_by_did, thread_at, created, rkey
	from reactions
	where reacted_by_did = ? and thread_at = ? and kind = ?`
	row := e.QueryRow(query, reactedByDid, threadAt, kind)

	var reaction models.Reaction
	var created string
	err := row.Scan(&reaction.ReactedByDid, &reaction.ThreadAt, &created, &reaction.Rkey)
	if err != nil {
		return nil, err
	}

	createdAtTime, err := time.Parse(time.RFC3339, created)
	if err != nil {
		log.Println("unable to determine followed at time")
		reaction.Created = time.Now()
	} else {
		reaction.Created = createdAtTime
	}

	return &reaction, nil
}

// Remove a reaction
func DeleteReaction(e Execer, reactedByDid string, threadAt syntax.ATURI, kind models.ReactionKind) error {
	_, err := e.Exec(`delete from reactions where reacted_by_did = ? and thread_at = ? and kind = ?`, reactedByDid, threadAt, kind)
	return err
}

// Remove a reaction
func DeleteReactionByRkey(e Execer, reactedByDid string, rkey string) error {
	_, err := e.Exec(`delete from reactions where reacted_by_did = ? and rkey = ?`, reactedByDid, rkey)
	return err
}

func GetReactionCount(e Execer, threadAt syntax.ATURI, kind models.ReactionKind) (int, error) {
	count := 0
	err := e.QueryRow(
		`select count(reacted_by_did) from reactions where thread_at = ? and kind = ?`, threadAt, kind).Scan(&count)
	if err != nil {
		return 0, err
	}
	return count, nil
}

func GetReactionCountMap(e Execer, threadAt syntax.ATURI) (map[models.ReactionKind]int, error) {
	countMap := map[models.ReactionKind]int{}
	for _, kind := range models.OrderedReactionKinds {
		count, err := GetReactionCount(e, threadAt, kind)
		if err != nil {
			return map[models.ReactionKind]int{}, nil
		}
		countMap[kind] = count
	}
	return countMap, nil
}

func GetReactionStatus(e Execer, userDid string, threadAt syntax.ATURI, kind models.ReactionKind) bool {
	if _, err := GetReaction(e, userDid, threadAt, kind); err != nil {
		return false
	} else {
		return true
	}
}

func GetReactionStatusMap(e Execer, userDid string, threadAt syntax.ATURI) map[models.ReactionKind]bool {
	statusMap := map[models.ReactionKind]bool{}
	for _, kind := range models.OrderedReactionKinds {
		count := GetReactionStatus(e, userDid, threadAt, kind)
		statusMap[kind] = count
	}
	return statusMap
}
