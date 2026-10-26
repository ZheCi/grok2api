package repository

// InventoryGroup 是库存分组。账号不在任何分组里时参与调度。
type InventoryGroup struct {
	ID   uint64 `json:"id,string"`
	Name string `json:"name"`
}
