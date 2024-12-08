package main

import (
	"database/sql"
	"errors"
	"log"
	"time"
)

func matchingAuto() {
	t1 := time.NewTicker(100 * time.Millisecond)
	t2 := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-t1.C:
			if err := matchingBigMoney(); err != nil {
				log.Printf("matchingOldest() failed: %v\n", err)
			}
		case <-t2.C:
			// NOTE: 古いやつを捌いてないと怒られる
			if err := matchingOldest(); err != nil {
				log.Printf("matchingOldest() failed: %v\n", err)
			}
		}
	}
}

func matchingOldest() error {
	ride := &Ride{}
	if err := db.Get(ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY ABS(pickup_latitude - destination_latitude) + ABS(pickup_longitude - destination_longitude) DESC LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	return matching(ride)
}

func matchingBigMoney() error {
	ride := &Ride{}
	if err := db.Get(ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY ABS(pickup_latitude - destination_latitude) + ABS(pickup_longitude - destination_longitude) DESC LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	return matching(ride)
}

func matching(ride *Ride) error {
	t := time.Now()

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

	if _, err := db.Exec("UPDATE rides SET chair_id = ? WHERE id = ?", matched.ID, ride.ID); err != nil {
		return err
	}

	log.Printf("matchingOne() matching done (%d ms)\n", time.Since(t).Milliseconds())

	return nil
}
