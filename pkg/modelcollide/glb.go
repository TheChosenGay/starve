// Package modelcollide 把客户端模型（.glb：二进制 glTF）转成服务端用的简化碰撞体。
//
// 流水线位置：客户端美术资产 →（本包）→ 形状参数（格心圆半径 / 占格盒尺寸 / 胶囊半径+身高）
// → 落到服务端配置（模板 collision_radius、建筑 width×height、生物 body_radius/body_height）。
//
// 只做"够用"的两件事：
//  1. 读出模型在世界空间的顶点（含节点变换；蒙皮网格按绑定姿势解算）；
//  2. 用明确的规则（取样高度带 + 百分位）算半径与尺寸——规则参数写在清单里，不藏在代码里。
//
// 不支持动画（只取绑定姿势）、不支持外部 .bin（.glb 必须自带 BIN chunk）。
package modelcollide

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

// Vec3 三维点。
type Vec3 struct{ X, Y, Z float64 }

const glbMagic = 0x46546C67 // "glTF"

// Mesh 是模型在世界空间的顶点集合（未缩放，单位=模型单位）。
type Mesh struct {
	Points []Vec3
	Min    Vec3
	Max    Vec3
	// Nodes 参与统计的节点数（诊断用：0 表示模型里没有网格）。
	Nodes int
}

// LoadGLB 读取 .glb 并返回世界空间顶点（含节点变换；蒙皮网格按绑定姿势解算）。
func LoadGLB(path string) (*Mesh, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	doc, bin, err := parseGLB(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	mesh := &Mesh{Min: Vec3{X: math.Inf(1), Y: math.Inf(1), Z: math.Inf(1)},
		Max: Vec3{X: math.Inf(-1), Y: math.Inf(-1), Z: math.Inf(-1)}}
	if err := mesh.collect(doc, bin); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(mesh.Points) == 0 {
		return nil, fmt.Errorf("%s: 模型里没有网格顶点", path)
	}
	return mesh, nil
}

// ---- glTF 文档结构（只声明本工具用到的字段）----

type gltfDoc struct {
	Scene  *int `json:"scene"`
	Scenes []struct {
		Nodes []int `json:"nodes"`
	} `json:"scenes"`
	Nodes  []gltfNode `json:"nodes"`
	Meshes []struct {
		Primitives []struct {
			Attributes map[string]int `json:"attributes"`
		} `json:"primitives"`
	} `json:"meshes"`
	Skins []struct {
		Joints              []int `json:"joints"`
		InverseBindMatrices *int  `json:"inverseBindMatrices"`
	} `json:"skins"`
	Accessors []gltfAccessor `json:"accessors"`
	Buffers   []struct {
		ByteLength int    `json:"byteLength"`
		URI        string `json:"uri"`
	} `json:"buffers"`
	BufferViews []struct {
		Buffer     int `json:"buffer"`
		ByteOffset int `json:"byteOffset"`
		ByteLength int `json:"byteLength"`
		ByteStride int `json:"byteStride"`
	} `json:"bufferViews"`
}

type gltfNode struct {
	Name        string    `json:"name"`
	Mesh        *int      `json:"mesh"`
	Skin        *int      `json:"skin"`
	Children    []int     `json:"children"`
	Matrix      []float64 `json:"matrix"`
	Translation []float64 `json:"translation"`
	Rotation    []float64 `json:"rotation"`
	Scale       []float64 `json:"scale"`
}

type gltfAccessor struct {
	BufferView    *int      `json:"bufferView"`
	ByteOffset    int       `json:"byteOffset"`
	ComponentType int       `json:"componentType"`
	Normalized    bool      `json:"normalized"`
	Count         int       `json:"count"`
	Type          string    `json:"type"`
	Min           []float64 `json:"min"`
	Max           []float64 `json:"max"`
}

// parseGLB 拆出 JSON chunk 与 BIN chunk。
func parseGLB(raw []byte) (*gltfDoc, []byte, error) {
	if len(raw) < 12 {
		return nil, nil, fmt.Errorf("文件太短，不是 glb")
	}
	if binary.LittleEndian.Uint32(raw[0:4]) != glbMagic {
		return nil, nil, fmt.Errorf("不是 glb（magic 不匹配；.gltf 请先导出成 .glb）")
	}
	if version := binary.LittleEndian.Uint32(raw[4:8]); version != 2 {
		return nil, nil, fmt.Errorf("不支持的 glb 版本 %d", version)
	}
	var doc *gltfDoc
	var bin []byte
	for off := 12; off+8 <= len(raw); {
		length := int(binary.LittleEndian.Uint32(raw[off : off+4]))
		kind := binary.LittleEndian.Uint32(raw[off+4 : off+8])
		start := off + 8
		end := start + length
		if end > len(raw) {
			return nil, nil, fmt.Errorf("chunk 长度越界")
		}
		switch kind {
		case 0x4E4F534A: // JSON
			d := &gltfDoc{}
			if err := json.Unmarshal(raw[start:end], d); err != nil {
				return nil, nil, fmt.Errorf("JSON chunk 解析失败: %w", err)
			}
			doc = d
		case 0x004E4942: // BIN
			bin = raw[start:end]
		}
		off = end
	}
	if doc == nil {
		return nil, nil, fmt.Errorf("缺少 JSON chunk")
	}
	if len(bin) == 0 {
		return nil, nil, fmt.Errorf("缺少 BIN chunk（外部 .bin 不支持）")
	}
	return doc, bin, nil
}

// collect 遍历场景图，把每个网格节点的顶点变换到世界空间。
func (m *Mesh) collect(doc *gltfDoc, bin []byte) error {
	roots := make([]int, 0, len(doc.Nodes))
	if doc.Scene != nil && *doc.Scene >= 0 && *doc.Scene < len(doc.Scenes) {
		roots = doc.Scenes[*doc.Scene].Nodes
	} else if len(doc.Scenes) > 0 {
		roots = doc.Scenes[0].Nodes
	} else {
		for i := range doc.Nodes {
			roots = append(roots, i)
		}
	}
	// 节点世界矩阵（先自顶向下算好，蒙皮要用）
	world := make([]Mat4, len(doc.Nodes))
	seen := make([]bool, len(doc.Nodes))
	var walk func(idx int, parent Mat4)
	walk = func(idx int, parent Mat4) {
		if idx < 0 || idx >= len(doc.Nodes) || seen[idx] {
			return
		}
		seen[idx] = true
		node := doc.Nodes[idx]
		world[idx] = parent.Mul(nodeMatrix(node))
		for _, child := range node.Children {
			walk(child, world[idx])
		}
	}
	for _, r := range roots {
		walk(r, Identity())
	}
	for i := range doc.Nodes {
		if !seen[i] {
			walk(i, Identity())
		}
	}

	for i, node := range doc.Nodes {
		if node.Mesh == nil || *node.Mesh < 0 || *node.Mesh >= len(doc.Meshes) {
			continue
		}
		mesh := doc.Meshes[*node.Mesh]
		var skinMatrices []Mat4
		if node.Skin != nil && *node.Skin >= 0 && *node.Skin < len(doc.Skins) {
			skinMatrices, _ = doc.skinMatrices(bin, *node.Skin, world)
		}
		before := len(m.Points)
		for _, prim := range mesh.Primitives {
			posIdx, ok := prim.Attributes["POSITION"]
			if !ok {
				continue
			}
			points, err := doc.readVec3(bin, posIdx)
			if err != nil {
				return err
			}
			if len(skinMatrices) > 0 {
				joints, weights, err := doc.readSkin(bin, prim.Attributes)
				if err != nil || len(joints) != len(points) {
					skinMatrices = nil // 蒙皮数据不完整：退回节点变换
				} else {
					for vi, v := range points {
						m.Points = append(m.Points, skinPoint(v, joints[vi], weights[vi], skinMatrices))
					}
					continue
				}
			}
			for _, v := range points {
				m.Points = append(m.Points, world[i].TransformPoint(v))
			}
		}
		if len(m.Points) > before {
			m.Nodes++
		}
	}
	for _, p := range m.Points {
		m.Min = Vec3{X: math.Min(m.Min.X, p.X), Y: math.Min(m.Min.Y, p.Y), Z: math.Min(m.Min.Z, p.Z)}
		m.Max = Vec3{X: math.Max(m.Max.X, p.X), Y: math.Max(m.Max.Y, p.Y), Z: math.Max(m.Max.Z, p.Z)}
	}
	return nil
}

// nodeMatrix 节点局部矩阵（matrix 优先，否则 TRS）。
func nodeMatrix(n gltfNode) Mat4 {
	if len(n.Matrix) == 16 {
		var m Mat4
		copy(m[:], n.Matrix)
		return m
	}
	t := [3]float64{0, 0, 0}
	q := [4]float64{0, 0, 0, 1}
	s := [3]float64{1, 1, 1}
	if len(n.Translation) == 3 {
		copy(t[:], n.Translation)
	}
	if len(n.Rotation) == 4 {
		copy(q[:], n.Rotation)
	}
	if len(n.Scale) == 3 {
		copy(s[:], n.Scale)
	}
	return trs(t, s, q)
}

// skinMatrices 绑定姿势下每个关节的矩阵：jointWorld · inverseBind。
func (d *gltfDoc) skinMatrices(bin []byte, skinIdx int, world []Mat4) ([]Mat4, error) {
	skin := d.Skins[skinIdx]
	out := make([]Mat4, len(skin.Joints))
	var ibms []Mat4
	if skin.InverseBindMatrices != nil {
		var err error
		if ibms, err = d.readMat4(bin, *skin.InverseBindMatrices); err != nil {
			return nil, err
		}
	}
	for i, joint := range skin.Joints {
		ibm := Identity()
		if i < len(ibms) {
			ibm = ibms[i]
		}
		if joint < 0 || joint >= len(world) {
			return nil, fmt.Errorf("关节下标越界")
		}
		out[i] = world[joint].Mul(ibm)
	}
	return out, nil
}

// skinPoint 绑定姿势蒙皮：Σ w_i · (jointMatrix_i · p)。
func skinPoint(p Vec3, joints [4]int, weights [4]float64, mats []Mat4) Vec3 {
	var out Vec3
	total := 0.0
	for i := 0; i < 4; i++ {
		w := weights[i]
		if w <= 0 || joints[i] < 0 || joints[i] >= len(mats) {
			continue
		}
		q := mats[joints[i]].TransformPoint(p)
		out.X += q.X * w
		out.Y += q.Y * w
		out.Z += q.Z * w
		total += w
	}
	if total <= 0 {
		return p
	}
	if math.Abs(total-1) > 1e-3 { // 权重和归一化
		out.X /= total
		out.Y /= total
		out.Z /= total
	}
	return out
}

// readSkin 读取 JOINTS_0 / WEIGHTS_0（缺失时返回 nil 让调用方退回节点变换）。
func (d *gltfDoc) readSkin(bin []byte, attrs map[string]int) ([][4]int, [][4]float64, error) {
	jointIdx, okJ := attrs["JOINTS_0"]
	weightIdx, okW := attrs["WEIGHTS_0"]
	if !okJ || !okW {
		return nil, nil, fmt.Errorf("缺 JOINTS_0/WEIGHTS_0")
	}
	count := d.Accessors[jointIdx].Count
	if wc := d.Accessors[weightIdx].Count; wc < count {
		count = wc
	}
	joints := make([][4]int, count)
	weights := make([][4]float64, count)
	rawJoints, err := d.readElements(bin, jointIdx)
	if err != nil {
		return nil, nil, err
	}
	rawWeights, err := d.readElements(bin, weightIdx)
	if err != nil {
		return nil, nil, err
	}
	for i := range joints {
		if i < len(rawJoints) {
			for c := 0; c < 4; c++ {
				joints[i][c] = int(rawJoints[i][c])
			}
		}
		if i < len(rawWeights) {
			copy(weights[i][:], rawWeights[i][:4])
		}
	}
	return joints, weights, nil
}

// readVec3 读取 VEC3 属性（POSITION）。
func (d *gltfDoc) readVec3(bin []byte, idx int) ([]Vec3, error) {
	raw, err := d.readElements(bin, idx)
	if err != nil {
		return nil, err
	}
	out := make([]Vec3, len(raw))
	for i, e := range raw {
		out[i] = Vec3{X: e[0], Y: e[1], Z: e[2]}
	}
	return out, nil
}

// readMat4 读取 MAT4 访问器（逆绑定矩阵）。
func (d *gltfDoc) readMat4(bin []byte, idx int) ([]Mat4, error) {
	raw, err := d.readElements(bin, idx)
	if err != nil {
		return nil, err
	}
	out := make([]Mat4, len(raw))
	for i, e := range raw {
		copy(out[i][:], e[:16])
	}
	return out, nil
}

// readElements 读取一个访问器的原始分量（按 byteStride 支持交错缓冲）。
func (d *gltfDoc) readElements(bin []byte, idx int) ([][16]float64, error) {
	if idx < 0 || idx >= len(d.Accessors) {
		return nil, fmt.Errorf("访问器下标越界")
	}
	acc := d.Accessors[idx]
	comps := componentsOf(acc.Type)
	size := componentSize(acc.ComponentType)
	if comps == 0 || size == 0 {
		return nil, fmt.Errorf("不支持的访问器类型 %s/%d", acc.Type, acc.ComponentType)
	}
	if acc.BufferView == nil {
		// 没有 bufferView = 全零（稀疏访问器之外的合法情况很少见）
		return make([][16]float64, acc.Count), nil
	}
	view := d.BufferViews[*acc.BufferView]
	if view.Buffer != 0 {
		return nil, fmt.Errorf("只支持 buffer 0（.glb 自带 BIN）")
	}
	start := view.ByteOffset + acc.ByteOffset
	stride := view.ByteStride
	if stride == 0 {
		stride = comps * size
	}
	out := make([][16]float64, acc.Count)
	for i := 0; i < acc.Count; i++ {
		base := start + i*stride
		if base+comps*size > len(bin) {
			return nil, fmt.Errorf("顶点数据越界")
		}
		for c := 0; c < comps && c < 16; c++ {
			out[i][c] = readComponent(bin[base+c*size:], acc.ComponentType, acc.Normalized)
		}
	}
	return out, nil
}

func componentsOf(typ string) int {
	switch typ {
	case "SCALAR":
		return 1
	case "VEC2":
		return 2
	case "VEC3":
		return 3
	case "VEC4", "MAT2":
		return 4
	case "MAT4":
		return 16
	}
	return 0
}

func componentSize(componentType int) int {
	switch componentType {
	case 5120, 5121: // BYTE / UNSIGNED_BYTE
		return 1
	case 5122, 5123: // SHORT / UNSIGNED_SHORT
		return 2
	case 5125, 5126: // UNSIGNED_INT / FLOAT
		return 4
	}
	return 0
}

func readComponent(b []byte, componentType int, normalized bool) float64 {
	switch componentType {
	case 5120:
		v := float64(int8(b[0]))
		if normalized {
			return math.Max(v/127, -1)
		}
		return v
	case 5121:
		v := float64(b[0])
		if normalized {
			return v / 255
		}
		return v
	case 5122:
		v := float64(int16(binary.LittleEndian.Uint16(b)))
		if normalized {
			return math.Max(v/32767, -1)
		}
		return v
	case 5123:
		v := float64(binary.LittleEndian.Uint16(b))
		if normalized {
			return v / 65535
		}
		return v
	case 5125:
		return float64(binary.LittleEndian.Uint32(b))
	case 5126:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b)))
	}
	return 0
}
