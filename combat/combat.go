package combat

import (
	"fmt"
	"log/slog"
	"math"
	"strconv"

	"github.com/volte6/mud/buffs"
	"github.com/volte6/mud/characters"
	"github.com/volte6/mud/items"
	"github.com/volte6/mud/mobs"
	"github.com/volte6/mud/races"
	"github.com/volte6/mud/rooms"
	"github.com/volte6/mud/skills"
	"github.com/volte6/mud/templates"
	"github.com/volte6/mud/users"
	"github.com/volte6/mud/util"
)

type SourceTarget string

const (
	User SourceTarget = "user"
	Mob  SourceTarget = "mob"
)

// Performs a combat round from a player to a mob
func AttackPlayerVsMob(user *users.UserRecord, mob *mobs.Mob) AttackResult {

	attackResult := calculateCombat(*user.Character, mob.Character, User, Mob)

	user.Character.ApplyHealthChange(attackResult.DamageToSource * -1)
	mob.Character.ApplyHealthChange(attackResult.DamageToTarget * -1)

	// Remember who has hit him
	if _, ok := mob.DamageTaken[user.UserId]; !ok {
		mob.DamageTaken[user.UserId] = 0
	}
	mob.DamageTaken[user.UserId] += attackResult.DamageToTarget

	return attackResult
}

// Performs a combat round from a player to a player
func AttackPlayerVsPlayer(userAtk *users.UserRecord, userDef *users.UserRecord) AttackResult {

	attackResult := calculateCombat(*userAtk.Character, *userDef.Character, User, User)

	userAtk.Character.ApplyHealthChange(attackResult.DamageToSource * -1)
	userDef.Character.ApplyHealthChange(attackResult.DamageToTarget * -1)

	return attackResult
}

// Performs a combat round from a mob to a player
func AttackMobVsPlayer(mob *mobs.Mob, user *users.UserRecord) AttackResult {

	attackResult := calculateCombat(mob.Character, *user.Character, Mob, User)

	mob.Character.ApplyHealthChange(attackResult.DamageToSource * -1)
	user.Character.ApplyHealthChange(attackResult.DamageToTarget * -1)

	return attackResult
}

// Performs a combat round from a mob to a mob
func AttackMobVsMob(mobAtk *mobs.Mob, mobDef *mobs.Mob) AttackResult {

	attackResult := calculateCombat(mobAtk.Character, mobDef.Character, Mob, User)

	mobAtk.Character.ApplyHealthChange(attackResult.DamageToSource * -1)
	mobDef.Character.ApplyHealthChange(attackResult.DamageToTarget * -1)

	// If attacking mob was player charmed, attribute damage done to that player
	if charmedUserId := mobAtk.Character.GetCharmedUserId(); charmedUserId > 0 {
		// Remember who has hit him
		if _, ok := mobDef.DamageTaken[charmedUserId]; !ok {
			mobDef.DamageTaken[charmedUserId] = 0
		}
		mobDef.DamageTaken[charmedUserId] += attackResult.DamageToTarget
	}

	return attackResult
}

func GetWaitMessages(stepType items.Intensity, sourceChar *characters.Character, targetChar *characters.Character, sourceType SourceTarget, targetType SourceTarget) AttackResult {

	attackResult := AttackResult{}

	msgs := items.GetPreAttackMessage(sourceChar.Equipment.Weapon.GetSpec().Subtype, stepType)

	var toAttackerMsg, toDefenderMsg, toAttackerRoomMsg, toDefenderRoomMsg items.ItemMessage

	tokenReplacements := map[items.TokenName]string{
		items.TokenItemName:     races.GetRace(sourceChar.RaceId).UnarmedName,
		items.TokenSource:       sourceChar.Name,
		items.TokenSourceType:   string(sourceType) + `name`,
		items.TokenTarget:       targetChar.Name,
		items.TokenTargetType:   string(targetType) + `name`,
		items.TokenUsesLeft:     `[Invalid]`,
		items.TokenDamage:       `[Invalid]`,
		items.TokenEntranceName: `unknown`,
		items.TokenExitName:     `unknown`,
	}

	if sourceChar.RoomId == targetChar.RoomId {
		toAttackerMsg = msgs.Together.ToAttacker.Get()
		toDefenderMsg = msgs.Together.ToDefender.Get()
		toAttackerRoomMsg = msgs.Together.ToRoom.Get()
		toDefenderRoomMsg = items.ItemMessage("")

	} else {

		toAttackerMsg = msgs.Separate.ToAttacker.Get()
		toDefenderMsg = msgs.Separate.ToDefender.Get()
		toAttackerRoomMsg = msgs.Separate.ToAttackerRoom.Get()
		toDefenderRoomMsg = msgs.Separate.ToDefenderRoom.Get()

		// Find the exit that leads to the target from the source (if any)
		if atkRoom := rooms.LoadRoom(sourceChar.RoomId); atkRoom != nil {
			tokenReplacements[items.TokenExitName] = `unknown`
			for exitName, exit := range atkRoom.Exits {
				if exit.RoomId == targetChar.RoomId {
					tokenReplacements[items.TokenExitName] = exitName
					break
				}
			}
		}
		// find the exit that leads to the source from the target (if any)
		if defRoom := rooms.LoadRoom(targetChar.RoomId); defRoom != nil {
			tokenReplacements[items.TokenEntranceName] = `unknown`
			for exitName, exit := range defRoom.Exits {
				if exit.RoomId == sourceChar.RoomId {
					tokenReplacements[items.TokenEntranceName] = exitName
					break
				}
			}
		}
	}

	if sourceChar.Equipment.Weapon.ItemId > 0 {
		tokenReplacements[items.TokenItemName] = sourceChar.Equipment.Weapon.DisplayName()
	}

	if sourceType == Mob {
		tokenReplacements[items.TokenSource] = sourceChar.GetMobName(0).String()
	}

	if targetType == Mob {
		tokenReplacements[items.TokenTarget] = targetChar.GetMobName(0).String()
	}

	for tokenName, tokenValue := range tokenReplacements {
		toAttackerMsg = toAttackerMsg.SetTokenValue(tokenName, tokenValue)
		toDefenderMsg = toDefenderMsg.SetTokenValue(tokenName, tokenValue)
		toAttackerRoomMsg = toAttackerRoomMsg.SetTokenValue(tokenName, tokenValue)
		if len(string(toDefenderRoomMsg)) > 0 {
			toDefenderRoomMsg = toAttackerRoomMsg.SetTokenValue(tokenName, tokenValue)
		}
	}

	if string(toAttackerMsg) != `` {
		attackResult.SendToSource(templates.AnsiParse(string(toAttackerMsg)))
	}

	if !sourceChar.HasBuffFlag(buffs.Hidden) {

		if string(toDefenderMsg) != `` {
			attackResult.SendToTarget(templates.AnsiParse(string(toDefenderMsg)))
		}

		if string(toAttackerRoomMsg) != `` {
			attackResult.SendToSourceRoom(templates.AnsiParse(string(toAttackerRoomMsg)))
		}

		if string(toDefenderRoomMsg) != `` {
			attackResult.SendToTargetRoom(templates.AnsiParse(string(toDefenderRoomMsg)))
		}

	}

	return attackResult
}

func calculateCombat(sourceChar characters.Character, targetChar characters.Character, sourceType SourceTarget, targetType SourceTarget) AttackResult {

	attackResult := AttackResult{}

	attackCount := int(math.Ceil(float64(sourceChar.Stats.Speed.ValueAdj-targetChar.Stats.Speed.ValueAdj) / 25))
	if attackCount < 1 {
		attackCount = 1
	}
	for i := 0; i < attackCount; i++ {

		slog.Info(`calculateCombat`, `Atk`, fmt.Sprintf(`%d/%d`, i+1, attackCount), `Source`, fmt.Sprintf(`%s (%s)`, sourceChar.Name, sourceType), `Target`, fmt.Sprintf(`%s (%s)`, targetChar.Name, targetType))

		attackWeapons := []items.Item{}

		dualWieldLevel := sourceChar.GetSkillLevel(skills.DualWield)

		if sourceChar.Equipment.Weapon.ItemId > 0 {
			attackWeapons = append(attackWeapons, sourceChar.Equipment.Weapon)
		}

		if sourceChar.Equipment.Offhand.ItemId > 0 && sourceChar.Equipment.Offhand.GetSpec().Type == items.Weapon {
			attackWeapons = append(attackWeapons, sourceChar.Equipment.Offhand)
		}

		// Put an empty weapon, so basically hands.
		if len(attackWeapons) == 0 {
			attackWeapons = append(attackWeapons, items.Item{
				ItemId: 0,
			})
		}

		if len(attackWeapons) > 1 {

			maxWeapons := 1
			if dualWieldLevel == 1 {
				maxWeapons = 1
			}

			if dualWieldLevel == 2 {

				roll := util.Rand(100)

				util.LogRoll(`Both Weapons`, roll, 50)

				if roll < 50 {
					maxWeapons = 2
				}
			}

			if dualWieldLevel >= 3 {
				maxWeapons = 2
			}

			// If two martial weapons are equipped, allow dual wielding even without the stat.
			if sourceChar.Equipment.Weapon.GetSpec().Subtype == items.Claws && sourceChar.Equipment.Offhand.GetSpec().Subtype == items.Claws {
				maxWeapons = 2
			}

			for len(attackWeapons) > maxWeapons {
				// Remove a random position
				rnd := util.Rand(len(attackWeapons))
				attackWeapons = append(attackWeapons[:rnd], attackWeapons[rnd+1:]...)
			}

		}

		attackMessagePrefix := ``
		// If they are backstabbing it's a free crit
		if sourceChar.Aggro.Type == characters.BackStab {
			attackResult.Crit = true
			attackMessagePrefix = `<ansi fg="magenta-bold">*[BACKSTAB]*</ansi> `
			// Failover to the default attack
			sourceChar.SetAggro(sourceChar.Aggro.UserId, sourceChar.Aggro.MobInstanceId, characters.DefaultAttack)
		}

		for _, weapon := range attackWeapons {

			penalty := 0
			if len(attackWeapons) > 1 {
				if dualWieldLevel < 4 {
					penalty = 35 //35% penalty to hit
				} else {
					penalty = 25 //25% penalty to hit
				}
			}

			// Set the default weapon info
			raceInfo := races.GetRace(sourceChar.RaceId)
			weaponName := raceInfo.UnarmedName
			weaponSubType := items.Generic

			// Get default racial dice rolls
			attacks, dCount, dSides, dBonus, critBuffs := sourceChar.GetDefaultDiceRoll()

			if weapon.ItemId > 0 {

				itemSpec := weapon.GetSpec()

				weaponName = weapon.DisplayName()

				weaponSubType = itemSpec.Subtype
				attacks, dCount, dSides, dBonus, critBuffs = weapon.GetDiceRoll()

			}

			slog.Info("DiceRolls", "attacks", attacks, "dCount", dCount, "dSides", dSides, "dBonus", dBonus, "critBuffs", critBuffs)

			// Individual weapons may get multiple attacks
			for j := 0; j < attacks; j++ {

				attackTargetDamage := 0
				attackTargetReduction := 0

				attackSourceDamage := 0
				attackSourceReduction := 0

				if Hits(sourceChar.Stats.Speed.ValueAdj, targetChar.Stats.Speed.ValueAdj, penalty) {
					attackResult.Hit = true
					attackTargetDamage = util.RollDice(dCount, dSides) + dBonus

					if attackResult.Crit || Crits(sourceChar, targetChar) {
						attackResult.Crit = true
						attackResult.BuffTarget = critBuffs
						attackTargetDamage += dCount*dSides + dBonus
					}
				}

				defenseAmt := util.Rand(targetChar.GetDefense())
				if defenseAmt > 0 {
					attackTargetReduction = int(math.Round((float64(defenseAmt) / 100) * float64(attackTargetDamage)))
					attackTargetDamage -= attackTargetReduction
				}

				defenseAmt = util.Rand(sourceChar.GetDefense())
				if defenseAmt > 0 {
					attackSourceReduction = int(math.Round((float64(defenseAmt) / 100) * float64(attackSourceDamage)))
					attackSourceDamage -= attackSourceReduction
				}

				// Calculate actual damage vs. possible damage pct
				pctDamage := math.Ceil(float64(attackTargetDamage) / float64(dCount*dSides+dBonus) * 100)

				msgs := items.GetAttackMessage(weaponSubType, int(pctDamage))

				var toAttackerMsg, toDefenderMsg, toAttackerRoomMsg, toDefenderRoomMsg items.ItemMessage

				tokenReplacements := map[items.TokenName]string{
					items.TokenItemName:     weaponName,
					items.TokenSource:       sourceChar.Name,
					items.TokenSourceType:   string(sourceType) + `name`,
					items.TokenTarget:       targetChar.Name,
					items.TokenTargetType:   string(targetType) + `name`,
					items.TokenUsesLeft:     `[Invalid]`,
					items.TokenDamage:       strconv.Itoa(attackTargetDamage),
					items.TokenEntranceName: `unknown`,
					items.TokenExitName:     `unknown`,
				}

				if sourceChar.RoomId == targetChar.RoomId {

					toAttackerMsg = msgs.Together.ToAttacker.Get()
					toDefenderMsg = msgs.Together.ToDefender.Get()
					toAttackerRoomMsg = msgs.Together.ToRoom.Get()
					toDefenderRoomMsg = items.ItemMessage("")

				} else {

					toAttackerMsg = msgs.Separate.ToAttacker.Get()
					toDefenderMsg = msgs.Separate.ToDefender.Get()
					toAttackerRoomMsg = msgs.Separate.ToAttackerRoom.Get()
					toDefenderRoomMsg = msgs.Separate.ToDefenderRoom.Get()

					slog.Error("toDefenderRoomMsg", "msg", toDefenderRoomMsg)
					// Find the exit that leads to the target from the source (if any)
					if atkRoom := rooms.LoadRoom(sourceChar.RoomId); atkRoom != nil {
						for exitName, exit := range atkRoom.Exits {
							if exit.RoomId == targetChar.RoomId {
								tokenReplacements[items.TokenExitName] = exitName
								break
							}
						}
					}
					// find the exit that leads to the source from the target (if any)
					if defRoom := rooms.LoadRoom(targetChar.RoomId); defRoom != nil {
						for exitName, exit := range defRoom.Exits {
							if exit.RoomId == sourceChar.RoomId {
								tokenReplacements[items.TokenEntranceName] = exitName
								break
							}
						}
					}
				}

				if sourceChar.Equipment.Weapon.ItemId > 0 {
					tokenReplacements[items.TokenItemName] = sourceChar.Equipment.Weapon.DisplayName()
				}

				if sourceType == Mob {
					tokenReplacements[items.TokenSource] = sourceChar.GetMobName(0).String()
				}

				if targetType == Mob {
					tokenReplacements[items.TokenTarget] = targetChar.GetMobName(0).String()
				}

				for tokenName, tokenValue := range tokenReplacements {
					toAttackerMsg = toAttackerMsg.SetTokenValue(tokenName, tokenValue)
					toDefenderMsg = toDefenderMsg.SetTokenValue(tokenName, tokenValue)
					toAttackerRoomMsg = toAttackerRoomMsg.SetTokenValue(tokenName, tokenValue)
					if len(string(toDefenderRoomMsg)) > 0 {
						toDefenderRoomMsg = toDefenderRoomMsg.SetTokenValue(tokenName, tokenValue)
					}
				}

				if attackResult.Crit {
					toAttackerMsg = items.ItemMessage(`<ansi fg="yellow-bold">***</ansi> ` + string(toAttackerMsg) + ` <ansi fg="yellow-bold">***</ansi>`)
					toDefenderMsg = items.ItemMessage(`<ansi fg="yellow-bold">***</ansi> ` + string(toDefenderMsg) + ` <ansi fg="yellow-bold">***</ansi>`)
					toAttackerRoomMsg = items.ItemMessage(`<ansi fg="yellow-bold">***</ansi> ` + string(toAttackerRoomMsg) + ` <ansi fg="yellow-bold">***</ansi>`)
					if len(string(toDefenderRoomMsg)) > 0 {
						toDefenderRoomMsg = items.ItemMessage(`<ansi fg="yellow-bold">***</ansi> ` + string(toDefenderRoomMsg) + ` <ansi fg="yellow-bold">***</ansi>`)
					}
				}

				if len(attackMessagePrefix) > 0 {
					toAttackerMsg = items.ItemMessage(attackMessagePrefix + string(toAttackerMsg))
					toDefenderMsg = items.ItemMessage(attackMessagePrefix + string(toDefenderMsg))
					toAttackerRoomMsg = items.ItemMessage(attackMessagePrefix + string(toAttackerRoomMsg))
					if len(string(toDefenderRoomMsg)) > 0 {
						toDefenderRoomMsg = items.ItemMessage(attackMessagePrefix + string(toDefenderRoomMsg))
					}
				}

				// Send to attacker
				attackerMsg := string(toAttackerMsg)
				if attackSourceDamage > 0 && attackSourceReduction > 0 {
					attackerMsg += fmt.Sprintf(` <ansi fg="white">[%d was blocked]</ansi>`, attackSourceReduction)
				}

				attackResult.SendToSource(
					templates.AnsiParse(string(
						attackerMsg,
					)),
				)

				// Send to victim
				defenderMsg := string(toDefenderMsg)
				if attackTargetDamage > 0 && attackTargetReduction > 0 {
					defenderMsg += fmt.Sprintf(` <ansi fg="red">[you blocked %d]</ansi>`, attackTargetReduction)
				}

				attackResult.SendToTarget(
					templates.AnsiParse(string(
						defenderMsg,
					)),
				)

				// Send to room
				attackResult.SendToSourceRoom(
					templates.AnsiParse(string(
						toAttackerRoomMsg.SetTokenValue(items.TokenTarget, targetChar.Name).
							SetTokenValue(items.TokenTargetType, string(targetType)),
					)),
				)

				// Send to defender room if separate
				if len(string(toDefenderRoomMsg)) > 0 {
					attackResult.SendToTargetRoom(
						templates.AnsiParse(string(
							toDefenderRoomMsg.SetTokenValue(items.TokenTarget, targetChar.Name).
								SetTokenValue(items.TokenTargetType, string(targetType)),
						)),
					)
				}

				attackResult.DamageToTarget += attackTargetDamage
				attackResult.DamageToTargetReduction += attackTargetReduction

				attackResult.DamageToSource += attackSourceDamage
				attackResult.DamageToSourceReduction += attackSourceReduction
			}

		}
	}
	return attackResult

}

// hit chance will be between 30 and 100
func hitChance(attackSpd, defendSpd int) int {
	atkPlusDef := float64(attackSpd + defendSpd)
	if atkPlusDef < 1 {
		atkPlusDef = 1
	}
	return 30 + int(float64(attackSpd)/atkPlusDef*70)
}

// Chance to hit
func Hits(attackSpd, defendSpd, hitModifier int) bool {
	// Attack speeds affect 90% of the hit chance
	toHit := hitChance(attackSpd, defendSpd)
	if hitModifier != 0 {
		toHit += hitModifier
	}

	// Always at leat a 5% chance
	if toHit < 5 {
		toHit = 5
	}

	// Always at most a 95% chance
	if toHit > 95 {
		toHit = 95
	}
	hitRoll := util.Rand(100)

	util.LogRoll(`Hits`, hitRoll, toHit)

	return hitRoll < toHit
}

// Whether they crit
func Crits(sourceChar characters.Character, targetChar characters.Character) bool {

	levelDiff := sourceChar.Level - targetChar.Level
	if levelDiff < 1 {
		levelDiff = 1
	}
	critChance := 5 + int(math.Round(float64(sourceChar.Stats.Strength.ValueAdj+sourceChar.Stats.Speed.ValueAdj)/float64(levelDiff)))

	if sourceChar.HasBuffFlag(buffs.Accuracy) {
		critChance *= 2
	}

	if targetChar.HasBuffFlag(buffs.Blink) {
		critChance /= 2
	}

	// Minimum 5% chance
	if critChance < 5 {
		critChance = 5
	}

	critRoll := util.Rand(100)

	util.LogRoll(`Crits`, critRoll, critChance)

	return critRoll < critChance
}
