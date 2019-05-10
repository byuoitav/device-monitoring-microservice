package then

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/byuoitav/common/nerr"
	"github.com/byuoitav/common/v2/events"
	"github.com/byuoitav/device-monitoring/actions/activesignal"
	"github.com/byuoitav/device-monitoring/actions/health"
	"github.com/byuoitav/device-monitoring/actions/ping"
	"github.com/byuoitav/device-monitoring/actions/roomstate"
	"github.com/byuoitav/device-monitoring/localsystem"
	"github.com/byuoitav/device-monitoring/messenger"
	"github.com/byuoitav/shipwright/actions/then"
	"go.uber.org/zap"
)

// TODO device health check

func init() {
	then.Add("ping-devices", pingDevices)
	then.Add("active-signal", activeSignal)
	then.Add("service-health-check", serviceHealthCheck)
	then.Add("state-update", stateUpdate)

	then.Add("hardware-info", hardwareInfo)
	then.Add("device-hardware-info", deviceHardwareInfo)
}

func pingDevices(ctx context.Context, with []byte, log *zap.SugaredLogger) *nerr.E {
	systemID, err := localsystem.SystemID()
	if err != nil {
		return err.Addf("unable to ping room")
	}

	roomID, err := localsystem.RoomID()
	if err != nil {
		return err.Addf("unable to ping devices")
	}

	// timeout if this takes longer than 30 seconds
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	results, err := ping.Room(ctx, roomID, ping.Config{
		Count: 3,
		Delay: 1 * time.Second,
	}, log)
	if err != nil {
		return err.Addf("unable to ping devices")
	}

	// push up results
	for id, result := range results {
		if len(result.Error) > 0 || result.PacketsLost > result.PacketsReceived {
			// unsuccessful
		} else {
			// successful
			messenger.Get().SendEvent(events.Event{
				GeneratingSystem: systemID,
				Timestamp:        time.Now(),
				EventTags: []string{
					events.Heartbeat,
				},
				AffectedRoom: events.GenerateBasicRoomInfo(roomID),
				TargetDevice: events.GenerateBasicDeviceInfo(id),
				Key:          "last-heartbeat",
				Value:        time.Now().Format(time.RFC3339),
				Data:         result,
			})
		}
	}

	// send one up for me too!
	messenger.Get().SendEvent(events.Event{
		GeneratingSystem: systemID,
		Timestamp:        time.Now(),
		EventTags: []string{
			events.Heartbeat,
		},
		AffectedRoom: events.GenerateBasicRoomInfo(roomID),
		TargetDevice: events.GenerateBasicDeviceInfo(systemID),
		Key:          "last-heartbeat",
		Value:        time.Now().Format(time.RFC3339),
	})

	return nil
}

func activeSignal(ctx context.Context, with []byte, log *zap.SugaredLogger) *nerr.E {
	systemID, err := localsystem.SystemID()
	if err != nil {
		return err.Addf("unable to get active signal")
	}

	// timeout if this takes longer than 30 seconds
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	active, err := activesignal.GetMap(ctx)
	if err != nil {
		return err.Addf("unable to get active signal")
	}

	// key is deviceID, value is true/false
	for k, v := range active {
		deviceInfo := events.GenerateBasicDeviceInfo(k)

		messenger.Get().SendEvent(events.Event{
			GeneratingSystem: systemID,
			Timestamp:        time.Now(),
			EventTags: []string{
				events.DetailState,
			},
			TargetDevice: deviceInfo,
			AffectedRoom: deviceInfo.BasicRoomInfo,
			Key:          "active-signal",
			Value:        fmt.Sprintf("%v", v),
		})
	}

	return nil
}

func serviceHealthCheck(ctx context.Context, with []byte, log *zap.SugaredLogger) *nerr.E {
	var configs []health.ServiceCheckConfig
	err := then.FillStructFromTemplate(ctx, string(with), log)
	if err != nil {
		return err.Addf("unable to check services")
	}

	systemID, err := localsystem.SystemID()
	if err != nil {
		return err.Addf("unable to get active signal")
	}
	deviceInfo := events.GenerateBasicDeviceInfo(systemID)

	// timeout if this takes longer than 30 seconds
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resps := health.CheckServices(ctx, configs)
	for i := range resps {
		messenger.Get().SendEvent(events.Event{
			GeneratingSystem: systemID,
			Timestamp:        time.Now(),
			EventTags: []string{
				events.Heartbeat,
				events.Mstatus,
			},
			TargetDevice: deviceInfo,
			AffectedRoom: deviceInfo.BasicRoomInfo,
			Key:          fmt.Sprintf("%v-status", resps[i].Name),
			Value:        fmt.Sprintf("%v", resps[i].StatusCode),
			Data:         resps[i],
		})
	}

	return nil
}

func stateUpdate(ctx context.Context, with []byte, log *zap.SugaredLogger) *nerr.E {
	systemID, err := localsystem.SystemID()
	if err != nil {
		return err.Addf("unable to send state update")
	}

	roomID, err := localsystem.RoomID()
	if err != nil {
		return err.Addf("unable to send state update")
	}

	// timeout if this takes longer than 30 seconds
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	state, err := roomstate.Get(ctx, roomID)
	if err != nil {
		return err.Addf("unable to send state update")
	}

	// base event
	event := events.Event{
		GeneratingSystem: systemID,
		Timestamp:        time.Now(),
		EventTags: []string{
			events.CoreState,
			events.AutoGenerated,
		},
		AffectedRoom: events.GenerateBasicRoomInfo(roomID),
	}

	sent := make(map[string]bool)
	for _, display := range state.Displays {
		if strings.Contains(display.Name, "-") {
			event.TargetDevice = events.GenerateBasicDeviceInfo(display.Name)
		} else {
			event.TargetDevice = events.GenerateBasicDeviceInfo(fmt.Sprintf("%v-%v", roomID, display.Name))
		}

		log.Infof("Reporting display state of %v", event.TargetDevice.DeviceID)

		if len(display.Power) > 0 {
			event.Key = "power"
			event.Value = display.Power
			messenger.Get().SendEvent(event)
		}

		if len(display.Input) > 0 {
			event.Key = "input"
			event.Value = display.Input
			messenger.Get().SendEvent(event)
		}

		if display.Blanked != nil {
			event.Key = "blanked"
			event.Value = fmt.Sprintf("%v", *display.Blanked)
			messenger.Get().SendEvent(event)
		}

		sent[display.Name] = true
	}

	for _, audio := range state.AudioDevices {
		if strings.Contains(audio.Name, "-") {
			event.TargetDevice = events.GenerateBasicDeviceInfo(audio.Name)
		} else {
			event.TargetDevice = events.GenerateBasicDeviceInfo(fmt.Sprintf("%v-%v", roomID, audio.Name))
		}

		log.Infof("Reporting audio state of %v", event.TargetDevice.DeviceID)

		if audio.Muted != nil {
			event.Key = "muted"
			event.Value = fmt.Sprintf("%v", *audio.Muted)
			messenger.Get().SendEvent(event)
		}

		if audio.Volume != nil {
			event.Key = "volume"
			event.Value = fmt.Sprintf("%v", *audio.Volume)
			messenger.Get().SendEvent(event)
		}

		// send common info if it hasn't already been sent
		if !sent[audio.Name] {
			if len(audio.Power) > 0 {
				event.Key = "power"
				event.Value = audio.Power
				messenger.Get().SendEvent(event)
			}

			if len(audio.Input) > 0 {
				event.Key = "input"
				event.Value = audio.Input
				messenger.Get().SendEvent(event)
			}
		}
	}

	return nil
}
