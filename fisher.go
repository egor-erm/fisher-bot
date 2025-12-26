package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
)

type jsonToken struct {
	Access  string `json:"access_token"`
	Type    string `json:"token_type"`
	Refresh string `json:"refresh_token"`
}

var token oauth2.TokenSource

func CacheTokenNotExists() bool {
	_, s := os.Stat("./token.json")
	return os.IsNotExist(s)
}

func InitializeToken() error {
	if CacheTokenNotExists() {
		fmt.Println("XBL: New Token")
		var err error
		Token, err := auth.RequestLiveTokenWriter(log.Writer())
		if err != nil {
			panic(err)
		}
		_ = WriteToken(Token)
		token = oauth2.StaticTokenSource(Token)
	} else {
		con, _ := ioutil.ReadFile("./token.json")
		data := &jsonToken{}
		err := json.Unmarshal(con, data)
		Token := &oauth2.Token{}

		Token.AccessToken = data.Access
		Token.RefreshToken = data.Refresh
		Token.TokenType = data.Type
		Token.Expiry = time.Now().AddDate(100, 0, 0)

		token = oauth2.StaticTokenSource(Token)
		log.Println("Cached XBL Token")
		if err != nil {
			return err
		}
	}
	return nil
}

func WriteToken(token *oauth2.Token) error {
	bytes, err := json.MarshalIndent(*token, "", "	")
	if err != nil {
		return err
	}
	_ = ioutil.WriteFile("./token.json", bytes, 0777)
	return nil
}

func main() {
	err := InitializeToken()
	if err != nil {
		panic(err)
	}

	address := "127.0.0.1:19135"
	if len(os.Args) == 2 {
		address = os.Args[1]
	}

	dialer := minecraft.Dialer{
		TokenSource: token,
	}

	conn, err := dialer.Dial("raknet", address)
	if err != nil {
		if strings.Contains(err.Error(), "4294967295") {
			fmt.Println("Токен устарел, удаляем его")
			err = os.Remove("token.json")
			if err != nil {
				fmt.Println(err)
			}
		}

		panic(err)
	}
	defer conn.Close()

	if err := conn.DoSpawn(); err != nil {
		panic(err)
	}

	player := NewPlayer()
	run := true
	go func(player *Player) {
		randoms := rand.New(rand.NewSource(time.Now().Unix()))

		for {
			random := 3 + randoms.Intn(3)
			time.Sleep(time.Second * time.Duration(random))

			if !player.online {
				continue
			}

			if player.timer > 30 && player.fishing {
				fishFalse(conn, player)
				fmt.Println("Долго ловит, выходим")
				run = false
				return
			}

			if player.fishing {
				player.timer++
			} else {
				if player.timer == 0 {
					time.Sleep(15 * time.Second)
					fishTrue(conn, player)
				}
			}
		}
	}(player)

	for run {
		pk, err := conn.ReadPacket()
		if err != nil {
			break
		}

		switch p := pk.(type) {
		case *packet.InventoryContent:
			if p.WindowID == 0 {
				copy(player.inventory, p.Content)
			}
		case *packet.AvailableCommands:
			if !player.online {
				player.online = true
			}
		case *packet.AddActor:
			if p.EntityType == "minecraft:fishing_hook" && player.fishing && player.hook_uid == 0 && player.hook_rid == 0 {
				fmt.Println("Заброс")
				player.hook_uid = p.EntityUniqueID
				player.hook_rid = p.EntityRuntimeID

				fishTrueContinue(conn, player)
			}
		case *packet.RemoveActor:
			if player.hook_uid == p.EntityUniqueID {
				fmt.Println("Вытащил")
				fishFalseLast(conn, player)
			}
		case *packet.ActorEvent:
			if player.fishing && p.EntityRuntimeID == player.hook_rid {
				switch player.stadies {
				case 0:
					if p.EventType == 14 {
						fmt.Println(player.stadies + 1)
						player.stadies++
					}
				case 1:
					if p.EventType == 12 {
						fmt.Println(player.stadies + 1)
						player.stadies++
					}
				case 2:
					if p.EventType == 13 {
						fmt.Println(player.stadies + 1)
						fishFalse(conn, player)
					}
				}
			}
		case *packet.InventoryTransaction:
			if !player.online {
				break
			}

			if len(p.Actions) == 3 && p.Actions[2].InventorySlot == 0 && p.Actions[2].WindowID == 0 {
				newItem := p.Actions[2].NewItem

				fishMobEquipment(conn, player, newItem)

				player.inventory[0].Stack = newItem.Stack

				break
			}

			for _, action := range p.Actions {
				if action.NewItem.Stack.ItemType.NetworkID != 0 {
					fmt.Println("Выловил: ID:", action.NewItem.Stack.ItemType.NetworkID)
				}
			}
		}
	}
}

func fishTrue(conn *minecraft.Conn, player *Player) {
	if err := conn.WritePacket(getAnimatePacket(conn, player)); err != nil {
		panic(err)
	}

	if err := conn.WritePacket(getFishPacket1(player)); err != nil {
		panic(err)
	}

	player.hook_uid = 0
	player.hook_rid = 0

	player.stadies = 0

	player.timer = 0

	player.fishing = true

	fmt.Println("Типа кинули")
}

func fishTrueContinue(conn *minecraft.Conn, player *Player) {
	if err := conn.WritePacket(getFishPacket2(player)); err != nil {
		panic(err)
	}
}

func fishFalse(conn *minecraft.Conn, player *Player) {
	if err := conn.WritePacket(getAnimatePacket(conn, player)); err != nil {
		panic(err)
	}

	if err := conn.WritePacket(getFishPacket1(player)); err != nil {
		panic(err)
	}

	player.stadies = 0

	player.timer = 0

	player.fishing = false
}

func fishFalseLast(conn *minecraft.Conn, player *Player) {
	if len(player.inventory[0].Stack.NBTData) >= 2 {
		if err := conn.WritePacket(getFishPacket3(player)); err != nil {
			panic(err)
		}
	}

	mob := &packet.MobEquipment{
		EntityRuntimeID: uint64(player.eid),
		NewItem: protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{
			ItemType:       protocol.ItemType{NetworkID: player.inventory[0].Stack.NetworkID, MetadataValue: player.inventory[0].Stack.MetadataValue},
			Count:          1,
			BlockRuntimeID: 0,
			NBTData:        player.inventory[0].Stack.NBTData,
			CanBePlacedOn:  []string{},
			CanBreak:       []string{},
			HasNetworkID:   true,
		}},
		InventorySlot: 0,
		HotBarSlot:    0,
		WindowID:      0,
	}

	if err := conn.WritePacket(mob); err != nil {
		panic(err)
	}
}

func fishMobEquipment(conn *minecraft.Conn, player *Player, item protocol.ItemInstance) {
	mob := &packet.MobEquipment{
		EntityRuntimeID: uint64(player.eid),
		NewItem: protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{
			ItemType:       protocol.ItemType{NetworkID: item.Stack.NetworkID, MetadataValue: item.Stack.MetadataValue},
			Count:          1,
			BlockRuntimeID: 0,
			NBTData:        item.Stack.NBTData,
			CanBePlacedOn:  []string{},
			CanBreak:       []string{},
			HasNetworkID:   true,
		}},
		InventorySlot: 0,
		HotBarSlot:    0,
		WindowID:      0,
	}

	if err := conn.WritePacket(mob); err != nil {
		panic(err)
	}

	player.timer = 0
}

func getFishPacket1(player *Player) *packet.InventoryTransaction {
	return &packet.InventoryTransaction{
		LegacySetItemSlots: []protocol.LegacySetItemSlot{{ContainerID: 0, Slots: []byte{0}}},
		Actions:            []protocol.InventoryAction{},
		TransactionData: &protocol.UseItemTransactionData{
			LegacyRequestID:    0,
			LegacySetItemSlots: []protocol.LegacySetItemSlot{},
			Actions:            []protocol.InventoryAction{},
			ActionType:         1,
			TriggerType:        0,
			BlockPosition:      protocol.BlockPos{0, 0, 0},
			BlockFace:          255,
			HotBarSlot:         0,
			HeldItem: protocol.ItemInstance{StackNetworkID: 0, Stack: protocol.ItemStack{
				ItemType:       protocol.ItemType{NetworkID: player.inventory[0].Stack.NetworkID, MetadataValue: player.inventory[0].Stack.MetadataValue},
				BlockRuntimeID: 0,
				Count:          1,
				NBTData:        player.inventory[0].Stack.NBTData,
				CanBePlacedOn:  []string{},
				CanBreak:       []string{},
				HasNetworkID:   false,
			}},
			Position:         player.position,
			ClickedPosition:  mgl32.Vec3{0, 0, 0},
			BlockRuntimeID:   0,
			ClientPrediction: 0,
		},
	}
}

func getFishPacket2(player *Player) *packet.InventoryTransaction {
	return &packet.InventoryTransaction{
		LegacySetItemSlots: []protocol.LegacySetItemSlot{{ContainerID: 0, Slots: []byte{0}}},
		Actions:            []protocol.InventoryAction{},
		TransactionData: &protocol.ReleaseItemTransactionData{
			ActionType: 0,
			HotBarSlot: 0,
			HeldItem: protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{
				ItemType:       protocol.ItemType{NetworkID: player.inventory[0].Stack.NetworkID, MetadataValue: player.inventory[0].Stack.MetadataValue},
				Count:          1,
				BlockRuntimeID: 0,
				NBTData:        player.inventory[0].Stack.NBTData,
				CanBePlacedOn:  []string{},
				CanBreak:       []string{},
				HasNetworkID:   true,
			}},
			HeadPosition: player.position,
		},
	}
}

func getFishPacket3(player *Player) *packet.InventoryTransaction {
	if value, ok := player.inventory[0].Stack.NBTData["Damage"]; ok {
		player.inventory[0].Stack.NBTData["Damage"] = value.(int32) - 1
	}

	return &packet.InventoryTransaction{
		LegacySetItemSlots: []protocol.LegacySetItemSlot{{ContainerID: 0, Slots: []byte{0}}},
		Actions:            []protocol.InventoryAction{},
		TransactionData: &protocol.ReleaseItemTransactionData{
			ActionType: 0,
			HotBarSlot: 0,
			HeldItem: protocol.ItemInstance{StackNetworkID: 1, Stack: protocol.ItemStack{
				ItemType:       protocol.ItemType{NetworkID: player.inventory[0].Stack.NetworkID, MetadataValue: player.inventory[0].Stack.MetadataValue},
				Count:          1,
				BlockRuntimeID: 0,
				NBTData:        player.inventory[0].Stack.NBTData,
				CanBePlacedOn:  []string{},
				CanBreak:       []string{},
				HasNetworkID:   true,
			}},
			HeadPosition: player.position,
		},
	}
}

func getAnimatePacket(conn *minecraft.Conn, player *Player) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: conn.GameData().EntityRuntimeID,
		Data:            0,
		SwingSource:     6,
	}
}

type Player struct {
	online    bool
	position  mgl32.Vec3
	fishing   bool
	hook_uid  int64
	hook_rid  uint64
	stadies   int8
	eid       uint64
	timer     int
	inventory []protocol.ItemInstance
}

func NewPlayer() *Player {
	return &Player{false, mgl32.Vec3{0, 0, 0}, false, 0, 0, 0, 0, 0, make([]protocol.ItemInstance, 36)}
}
