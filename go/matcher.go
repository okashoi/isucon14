package main

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

func matchingAuto() {
	t := time.NewTicker(100 * time.Millisecond)
	for {
		select {
		case <-t.C:
			_ = matchingOne()
		}
	}
}

func matchingOne() error {
	t := time.Now()
	defer func() {
		log.Printf("matchingOne() done (%s ms)\n", time.Since(t).Milliseconds())
	}()

	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	ride := &Ride{}
	if err := db.Get(ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	matched := &Chair{}
	empty := false
	for i := 0; i < 10; i++ {
		if err := db.Get(matched, "SELECT * FROM chairs INNER JOIN (SELECT id FROM chairs WHERE is_active = TRUE ORDER BY RAND() LIMIT 1) AS tmp ON chairs.id = tmp.id LIMIT 1"); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}

		if err := db.Get(&empty, "SELECT COUNT(*) = 0 FROM (SELECT COUNT(chair_sent_at) = 6 AS completed FROM ride_statuses WHERE ride_id IN (SELECT id FROM rides WHERE chair_id = ?) GROUP BY ride_id) is_completed WHERE completed = FALSE", matched.ID); err != nil {
			return err
		}
		if empty {
			break
		}
	}
	if !empty {
		return nil
	}

	_, err := db.Exec("UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID)
	return err
}
