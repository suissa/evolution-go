package whatsmeow_service

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestExtractLiveLocationPointReadsStartCoordinates(t *testing.T) {
	latitude := -23.5505
	longitude := -46.6333
	accuracy := uint32(12)
	comment := "Entrega a caminho"
	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(latitude),
			DegreesLongitude: proto.Float64(longitude),
			IsLive:            proto.Bool(true),
			AccuracyInMeters:  &accuracy,
			Comment:           &comment,
		},
	}

	point, ok := extractLiveLocationPoint(msg)
	if !ok {
		t.Fatal("expected live-location start to be detected")
	}
	if point.Update {
		t.Fatal("initial live-location message must not be marked as an update")
	}
	if point.Latitude != latitude || point.Longitude != longitude {
		t.Fatalf("unexpected coordinates: got (%v, %v), want (%v, %v)", point.Latitude, point.Longitude, latitude, longitude)
	}
	if point.AccuracyInMeters == nil || *point.AccuracyInMeters != accuracy {
		t.Fatalf("unexpected accuracy: %#v", point.AccuracyInMeters)
	}
	if point.Caption == nil || *point.Caption != comment {
		t.Fatalf("unexpected caption: %#v", point.Caption)
	}
}

func TestExtractLiveLocationPointReadsUpdates(t *testing.T) {
	latitude := 0.0 // Zero is a valid coordinate and must not be treated as missing.
	longitude := 0.0
	sequence := int64(7)
	offset := uint32(42)
	speed := float32(3.5)
	heading := uint32(90)
	msg := &waE2E.Message{
		LiveLocationMessage: &waE2E.LiveLocationMessage{
			DegreesLatitude:                   proto.Float64(latitude),
			DegreesLongitude:                  proto.Float64(longitude),
			SequenceNumber:                    proto.Int64(sequence),
			TimeOffset:                        proto.Uint32(offset),
			SpeedInMps:                        &speed,
			DegreesClockwiseFromMagneticNorth: &heading,
		},
	}

	point, ok := extractLiveLocationPoint(msg)
	if !ok {
		t.Fatal("expected live-location update to be detected")
	}
	if !point.Update {
		t.Fatal("live-location update must be marked as an update")
	}
	if point.Latitude != latitude || point.Longitude != longitude {
		t.Fatalf("unexpected coordinates: got (%v, %v)", point.Latitude, point.Longitude)
	}
	if point.SequenceNumber != sequence || point.TimeOffset != offset {
		t.Fatalf("unexpected sequence/time offset: got (%d, %d)", point.SequenceNumber, point.TimeOffset)
	}
	if point.SpeedInMps == nil || *point.SpeedInMps != speed {
		t.Fatalf("unexpected speed: %#v", point.SpeedInMps)
	}
	if point.DegreesClockwiseFromMagneticNorth == nil || *point.DegreesClockwiseFromMagneticNorth != heading {
		t.Fatalf("unexpected heading: %#v", point.DegreesClockwiseFromMagneticNorth)
	}
}

func TestExtractLiveLocationPointIgnoresStaticLocation(t *testing.T) {
	latitude := -23.5505
	longitude := -46.6333
	msg := &waE2E.Message{
		LocationMessage: &waE2E.LocationMessage{
			DegreesLatitude:  proto.Float64(latitude),
			DegreesLongitude: proto.Float64(longitude),
			IsLive:            proto.Bool(false),
		},
	}

	if _, ok := extractLiveLocationPoint(msg); ok {
		t.Fatal("static location must not be treated as live location")
	}
}
