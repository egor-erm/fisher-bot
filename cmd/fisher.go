package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-gl/mathgl/mgl32"
	"github.com/sandertv/gophertunnel/minecraft"
	"github.com/sandertv/gophertunnel/minecraft/auth"
	"github.com/sandertv/gophertunnel/minecraft/protocol"
	"github.com/sandertv/gophertunnel/minecraft/protocol/packet"
	"golang.org/x/oauth2"
)

func main() {
	err := InitializeToken()
	if err != nil {
		panic(err)
	}

	address := "127.0.0.1:19132"
	if len(os.Args) == 2 {
		address = os.Args[1]
	}

	dialer := minecraft.Dialer{
		TokenSource: token,
	}

	conn, err := dialer.Dial("raknet", address)
	if err != nil {
		if strings.Contains(err.Error(), "2148916276") {
			fmt.Println("The token is outdated, we are deleting it...")
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

	wg := &sync.WaitGroup{}
	wg.Add(1)

	player := NewPlayer(conn.GameData().PlayerPosition)

	go ticker(wg, conn, player)
	go handle(wg, conn, player)

	wg.Wait()
}

func ticker(wg *sync.WaitGroup, conn *minecraft.Conn, player *Player) {
	for {
		if !player.online {
			continue
		}

		if player.timer-player.startFishing > 35*20 && player.fishing {
			fishFalse(conn, player)
			fmt.Println("It takes a long time to catch, we log out of the server...")
			break
		}

		if player.timer == 0 {
			sbls := &packet.ServerBoundLoadingScreen{
				Type:            1,
				LoadingScreenID: protocol.Optional[uint32]{},
			}
			conn.WritePacket(sbls)
		}

		if player.timer == 10 {
			offsets := make([]protocol.SubChunkOffset, 0)
			for x := int8(-2); x <= 2; x++ {
				for y := int8(-1); y <= 0; y++ {
					for z := int8(-2); z <= 2; z++ {
						offsets = append(offsets, protocol.SubChunkOffset{x, y, z})
					}
				}
			}

			scr := &packet.SubChunkRequest{
				Dimension: 0,
				Position:  protocol.SubChunkPos{0, 0, 0},
				Offsets:   offsets,
			}
			conn.WritePacket(scr)
		}

		if player.timer == 15 {
			sbls := &packet.ServerBoundLoadingScreen{
				Type:            2,
				LoadingScreenID: protocol.Optional[uint32]{},
			}
			conn.WritePacket(sbls)
		}

		au := &packet.PlayerAuthInput{
			Pitch:                  conn.GameData().Pitch,
			Yaw:                    conn.GameData().Yaw,
			Position:               conn.GameData().PlayerPosition,
			MoveVector:             mgl32.Vec2{0, 0},
			HeadYaw:                0,
			InputMode:              1,
			PlayMode:               2,
			InteractionModel:       0,
			InteractPitch:          0,
			InteractYaw:            0,
			Tick:                   player.timer,
			Delta:                  mgl32.Vec3{0, 0, 0},
			ItemInteractionData:    protocol.UseItemTransactionData{},
			ItemStackRequest:       protocol.ItemStackRequest{},
			BlockActions:           []protocol.PlayerBlockAction{},
			VehicleRotation:        mgl32.Vec2{0, 0},
			ClientPredictedVehicle: 0,
			AnalogueMoveVector:     mgl32.Vec2{0, 0},
			CameraOrientation:      mgl32.Vec3{0, 0, 0},
			RawMoveVector:          mgl32.Vec2{0, 0},
		}

		if (!player.fishing && player.timer != 0 && player.timer%60 == 0) || player.needPull {
			if !player.fishing && player.timer != 0 && player.timer%60 == 0 {
				fishTrue(conn, player)
			} else {
				player.needPull = false
			}

			au.InputData = getInputData(true)
		} else {
			au.InputData = getInputData(false)
		}

		conn.WritePacket(au)

		player.timer++
		time.Sleep(50 * time.Millisecond)
	}

	wg.Done()
}

func handle(wg *sync.WaitGroup, conn *minecraft.Conn, player *Player) {
	for {
		pk, err := conn.ReadPacket()
		if err != nil {
			fmt.Println(err)
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
			if p.EntityType == "minecraft:fishing_hook" && player.fishing && player.hookUid == 0 && player.hookRid == 0 {
				fmt.Println("Casting a fishing rod...")
				player.hookUid = p.EntityUniqueID
				player.hookRid = p.EntityRuntimeID
			}
		case *packet.RemoveActor:
			if player.hookUid == p.EntityUniqueID {
				fmt.Println("Pulling out the fishing rod...")
			}
		case *packet.ActorEvent:
			if player.fishing && p.EntityRuntimeID == player.hookRid {
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
						player.needPull = true
					}
				}
			}
		case *packet.InventoryTransaction:
			if !player.online {
				break
			}

			if len(p.Actions) == 3 && p.Actions[2].InventorySlot == 0 && p.Actions[2].WindowID == 0 {
				newItem := p.Actions[2].NewItem
				player.inventory[0].Stack = newItem.Stack
				break
			}

			for _, action := range p.Actions {
				if action.NewItem.Stack.ItemType.NetworkID != 0 {
					fmt.Println("Fish out: ID:", action.NewItem.Stack.ItemType.NetworkID)
				}
			}
		}
	}

	wg.Done()
}

func getInputData(full bool) protocol.Bitset {
	var data []bool
	if full {
		data = []bool{false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, true, false, true, false, false, true, false, false, false, false, false, false, false, false, false, false, false}
	} else {
		data = []bool{false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, false, true, false, true, false, false, false, false, false, false, false, false, false, false, false, false, false, false}
	}

	inputData := protocol.NewBitset(len(data))
	for i, value := range data {
		if value {
			inputData.Set(i)
		}
	}

	return inputData
}

func fishTrue(conn *minecraft.Conn, player *Player) {
	if err := conn.WritePacket(getAnimatePacket(conn)); err != nil {
		panic(err)
	}

	if err := conn.WritePacket(getFishPacket(player)); err != nil {
		panic(err)
	}

	player.hookUid = 0
	player.hookRid = 0
	player.stadies = 0

	player.startFishing = player.timer
	player.fishing = true
}

func fishFalse(conn *minecraft.Conn, player *Player) {
	if err := conn.WritePacket(getAnimatePacket(conn)); err != nil {
		panic(err)
	}

	if err := conn.WritePacket(getFishPacket(player)); err != nil {
		panic(err)
	}

	player.stadies = 0
	player.fishing = false
}

func getFishPacket(player *Player) *packet.InventoryTransaction {
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

func getAnimatePacket(conn *minecraft.Conn) *packet.Animate {
	return &packet.Animate{
		ActionType:      packet.AnimateActionSwingArm,
		EntityRuntimeID: conn.GameData().EntityRuntimeID,
		Data:            0,
		SwingSource:     6,
	}
}

type Player struct {
	online       bool
	position     mgl32.Vec3
	fishing      bool
	needPull     bool
	hookUid      int64
	hookRid      uint64
	stadies      int8
	eid          uint64
	timer        uint64
	startFishing uint64
	inventory    []protocol.ItemInstance
}

func NewPlayer(pos mgl32.Vec3) *Player {
	return &Player{
		false, pos, false, false, 0, 0, 0, 0, 0, 0, make([]protocol.ItemInstance, 36),
	}
}

type jsonToken struct {
	Access  string `json:"access_token"`
	Type    string `json:"token_type"`
	Refresh string `json:"refresh_token"`
}

var token oauth2.TokenSource

func CacheTokenNotExists() bool {
	_, err := os.Stat("./token.json")
	return os.IsNotExist(err)
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
		file, err := os.Open("./token.json")
		if err != nil {
			return err
		}
		defer file.Close()

		data := &jsonToken{}
		err = json.NewDecoder(file).Decode(data)

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
	bytes, err := json.MarshalIndent(token, "", "\t")
	if err != nil {
		return err
	}

	file, err := os.Create("./token.json")
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.Write(bytes)
	if err != nil {
		return err
	}

	return file.Sync()
}
