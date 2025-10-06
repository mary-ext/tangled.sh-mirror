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

func GetReactionMap(e Execer, userLimit int, threadAt syntax.ATURI) (map[models.ReactionKind]models.ReactionDisplayData, error) {
	query := `
	select kind, reacted_by_did,
	       row_number() over (partition by kind order by created asc) as rn,
	       count(*) over (partition by kind) as total
	from reactions
	where thread_at = ?
	order by kind, created asc`

	rows, err := e.Query(query, threadAt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	reactionMap := map[models.ReactionKind]models.ReactionDisplayData{}
	for _, kind := range models.OrderedReactionKinds {
		reactionMap[kind] = models.ReactionDisplayData{Count: 0, Users: []string{}}
	}

	for rows.Next() {
		var kind models.ReactionKind
		var did string
		var rn, total int
		if err := rows.Scan(&kind, &did, &rn, &total); err != nil {
			return nil, err
		}

		data := reactionMap[kind]
		data.Count = total
		if userLimit > 0 && rn <= userLimit {
			data.Users = append(data.Users, did)
		}
		reactionMap[kind] = data
	}

	return reactionMap, rows.Err()
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
