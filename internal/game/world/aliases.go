package world

import (
	"starve/internal/game/config"
	"starve/internal/game/worldmap"
)

// 类型已迁至 config / worldmap 包，这里保留 world 下的旧名（类型别名，等价引用），
// 避免命令/存档/测试等调用方大范围改动。新代码建议直接引用 config / worldmap。
type (
	WorldConfig  = config.WorldConfig
	GameConfig   = config.GameConfig
	ItemTemplate = config.ItemTemplate
	ToolSpec     = config.ToolSpec
	UseEffect    = config.UseEffect
	DropEntry    = config.DropEntry
	Recipe       = config.Recipe

	MapSpec            = worldmap.MapSpec
	HeightSpec         = worldmap.HeightSpec
	HandplacedSpec     = worldmap.HandplacedSpec
	ResourceSeed       = worldmap.ResourceSeed
	StationSeed        = worldmap.StationSeed
	RevivalStatueSeed  = worldmap.RevivalStatueSeed
	SeededResource     = worldmap.SeededResource
	LootSeed           = worldmap.LootSeed
	EffectTileSeed     = worldmap.EffectTileSeed
	EffectInstanceSeed = worldmap.EffectInstanceSeed
	EmitterSeed        = worldmap.EmitterSeed
	ScatterRule        = worldmap.ScatterRule
	MapResult          = worldmap.MapResult
	MapGenerator       = worldmap.MapGenerator
	MapData            = worldmap.MapData
	BiomeType          = worldmap.BiomeType
	BiomeSpec          = worldmap.BiomeSpec
	BiomeTerrain       = worldmap.BiomeTerrain
	BiomeResource      = worldmap.BiomeResource
	BiomeTileEffect    = worldmap.BiomeTileEffect
	RegionLayoutSpec   = worldmap.RegionLayoutSpec
	RegionRule         = worldmap.RegionRule
	RegionInstance     = worldmap.RegionInstance
	WeatherBias        = worldmap.WeatherBias
)
