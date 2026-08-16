package task

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
)

type persona struct {
	Name  string
	Color string
}

var defaultPersonas = []persona{
	{Name: "户山香澄", Color: "#FF5522"},
	{Name: "花园多惠", Color: "#00AAFF"},
	{Name: "牛込里美", Color: "#FF55BB"},
	{Name: "山吹沙綾", Color: "#FFCC11"},
	{Name: "市谷有咲", Color: "#AA66DD"},
	{Name: "美竹兰", Color: "#EE2233"},
	{Name: "青叶摩卡", Color: "#8899AA"},
	{Name: "上原绯玛丽", Color: "#FF9999"},
	{Name: "宇田川巴", Color: "#BB0033"},
	{Name: "羽泽鸫", Color: "#FFEE88"},
	{Name: "丸山彩", Color: "#FF88BB"},
	{Name: "冰川日菜", Color: "#55DDEE"},
	{Name: "白鹭千圣", Color: "#EEAA22"},
	{Name: "大和麻弥", Color: "#77BB44"},
	{Name: "若宫伊芙", Color: "#88DDFF"},
	{Name: "凑友希那", Color: "#881188"},
	{Name: "冰川纱夜", Color: "#00AABB"},
	{Name: "今井莉莎", Color: "#DD2200"},
	{Name: "宇田川亚子", Color: "#DD0088"},
	{Name: "白金燐子", Color: "#BBBBBB"},
	{Name: "弦卷心", Color: "#FFEE33"},
	{Name: "濑田薰", Color: "#7722EE"},
	{Name: "北泽育美", Color: "#FF7722"},
	{Name: "松原花音", Color: "#44DDFF"},
	{Name: "奥泽美咲（米歇尔）", Color: "#006699"},
	{Name: "仓田真白", Color: "#6677CC"},
	{Name: "桐谷透子", Color: "#EE6666"},
	{Name: "广町七深", Color: "#EE7744"},
	{Name: "二叶筑", Color: "#EE7788"},
	{Name: "八潮瑠唯", Color: "#669988"},
	{Name: "LAYER", Color: "#6600EE"},
	{Name: "LOCK", Color: "#EE6622"},
	{Name: "MASKING", Color: "#FF0066"},
	{Name: "PAREO", Color: "#FF66CC"},
	{Name: "CHU²", Color: "#00EEDD"},
	{Name: "高松灯", Color: "#CC0033"},
	{Name: "千早爱音", Color: "#FF99CC"},
	{Name: "要乐奈", Color: "#FFCC00"},
	{Name: "长崎爽世", Color: "#6666FF"},
	{Name: "椎名立希", Color: "#00CC99"},
}

func (m *Manager) assignPersona(ctx context.Context) (persona, error) {
	occupied, err := m.occupiedPersonaNames(ctx)
	if err != nil {
		return persona{}, err
	}

	available := make([]persona, 0, len(defaultPersonas))
	for _, p := range defaultPersonas {
		if _, ok := occupied[p.Name]; ok {
			continue
		}
		available = append(available, p)
	}
	if len(available) == 0 {
		return persona{}, fmt.Errorf("no idle task names available")
	}

	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(available))))
	if err != nil {
		return persona{}, fmt.Errorf("pick task name: %w", err)
	}
	return available[n.Int64()], nil
}

func (m *Manager) occupiedPersonaNames(ctx context.Context) (map[string]struct{}, error) {
	occupied := make(map[string]struct{})
	if m == nil {
		return occupied, nil
	}

	m.mu.RLock()
	for _, task := range m.tasks {
		markOccupiedPersona(occupied, task)
	}
	m.mu.RUnlock()

	diskTasks, err := m.registry.listTasks(ctx)
	if err != nil {
		return nil, err
	}
	for _, task := range diskTasks {
		markOccupiedPersona(occupied, task)
	}
	return occupied, nil
}

func markOccupiedPersona(occupied map[string]struct{}, task TaskSnapshot) {
	if task.Status != TaskRunning {
		return
	}
	name := strings.TrimSpace(task.Name)
	if name == "" {
		return
	}
	occupied[name] = struct{}{}
}
