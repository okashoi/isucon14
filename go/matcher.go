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
			if err := matchingOne(); err != nil {
				log.Println("matchingAuto() failed", err)
			}
		}
	}
}

func matchingOne() error {
	t := time.Now()

	// MEMO: 一旦最も待たせているリクエストに適当な空いている椅子マッチさせる実装とする。おそらくもっといい方法があるはず…
	ride := &Ride{}
	if err := db.Get(ride, `SELECT * FROM rides WHERE chair_id IS NULL ORDER BY created_at LIMIT 1`); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}

	var matchedChairId string
	if err := db.Get(&matchedChairId, "SELECT id FROM chairs WHERE is_active = 1 AND id NOT IN (SELECT chair_id FROM incompleted_chairs) LIMIT 1", ride.PickupLatitude, ride.PickupLongitude); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Println("matchingOne() no chair found")
			return nil
		}
		return err
	}

	if _, err := db.Exec("UPDATE rides SET chair_id = ? WHERE id = ? AND chair_id IS NULL", matchedChairId, ride.ID); err != nil {
		return err
	}

	log.Printf("matchingOne() matching done (%d ms)\n", time.Since(t).Milliseconds())

	return nil
}
