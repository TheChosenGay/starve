package components

import game "starve/pkg/proto/game"

// Season 季节（单一事实来源 = proto 枚举；由世界时钟推导，随存档恢复）。
type Season = game.Season

// 常用季节常量。
const (
	SeasonSpring = game.Season_SEASON_SPRING
	SeasonSummer = game.Season_SEASON_SUMMER
	SeasonAutumn = game.Season_SEASON_AUTUMN
	SeasonWinter = game.Season_SEASON_WINTER
)
