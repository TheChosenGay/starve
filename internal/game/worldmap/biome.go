package worldmap

import game "starve/pkg/proto/game"

// BiomeType 生物群系类型（单一事实来源 = proto 枚举；配置用名字，内部用枚举）。
type BiomeType = game.BiomeType

// 常用生物群系常量。
const (
	BiomeGrassland = game.BiomeType_BIOME_TYPE_GRASSLAND
	BiomeForest    = game.BiomeType_BIOME_TYPE_FOREST
	BiomeSwamp     = game.BiomeType_BIOME_TYPE_SWAMP
	BiomeSnowland  = game.BiomeType_BIOME_TYPE_SNOWLAND
	BiomeMine      = game.BiomeType_BIOME_TYPE_MINE
)
