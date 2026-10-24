package itemsync

import (
	"log"

	"elb/ui/internal/daemonclient"
	"elb/ui/internal/store"
)

func Sync(daemon *daemonclient.Client, st *store.Store, gameName string) bool {
	items, err := daemon.Items()
	if err != nil {
		log.Printf("items: sync failed (daemon unreachable?): %v", err)
		return false
	}
	existing, err := st.ItemMappingCount()
	if err != nil {
		log.Printf("items: could not read current cache size: %v", err)
		return false
	}
	if existing > 0 && len(items) < existing/2 {
		log.Printf("items: new daemon response (%d entries) is noticeably smaller than the current cache (%d) — looks like a degraded fallback (mod unreachable?), cache NOT updated", len(items), existing)
		return false
	}
	if err := st.SaveItemMapping(items, gameName); err != nil {
		log.Printf("items: saving mapping: %v", err)
		return false
	}
	log.Printf("items: synced %d entries", len(items))
	return true
}
