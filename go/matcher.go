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
		// マンハッタン距離が最も近い椅子
		if err := db.Get(matched, "SELECT * FROM chairs WHERE id = (SELECT chair_id FROM active_chair_locations ORDER BY ABS(latitude - ?) + ABS(longitude - ?) LIMIT 1)", ride.PickupLatitude, ride.PickupLongitude); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}

		// ↓ これ何やってるのか分からない。わかった人おしえて by pinkumohikan
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

	if _, err := db.Exec("UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
		return err
	}

	log.Printf("matchingOne() matching done (%d ms)\n", time.Since(t).Milliseconds())

	return nil
}
