// Command configcheck validates the effective production configuration without
// starting the actor engine or opening network ports.
package main

import (
	"fmt"
	"log"

	"starve/internal/game/config"
)

func main() {
	manager := config.NewConfigManagerFromEnv()
	gameConfig, err := manager.Load()
	if err != nil {
		log.Fatalf("invalid game configuration: %v", err)
	}
	fmt.Printf(
		"configuration valid: templates=%d recipes=%d stations=%d biomes=%d creatures=%d buildings=%d map=%t\n",
		len(gameConfig.Templates),
		len(gameConfig.Recipes),
		len(gameConfig.Stations),
		len(gameConfig.Biomes),
		len(gameConfig.Creatures),
		len(gameConfig.Buildings),
		gameConfig.MapSpec != nil,
	)
}
