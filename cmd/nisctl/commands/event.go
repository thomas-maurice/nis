package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/spf13/cobra"
	nisv1 "github.com/thomas-maurice/nis/gen/nis/v1"
	"github.com/thomas-maurice/nis/internal/client"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var eventCmd = &cobra.Command{
	Use:   "event",
	Short: "View audit log events",
}

var eventListCmd = &cobra.Command{
	Use:   "list",
	Short: "List audit log events",
	RunE:  runEventList,
}

var eventGetCmd = &cobra.Command{
	Use:   "get ID",
	Short: "Get a single audit log event",
	Args:  cobra.ExactArgs(1),
	RunE:  runEventGet,
}

var (
	eventTypes       []string
	eventResourceType string
	eventResourceID   string
	eventOperator     string
	eventSince        time.Duration
	eventLimit        int
	eventCursor       string
)

func init() {
	rootCmd.AddCommand(eventCmd)
	eventCmd.AddCommand(eventListCmd)
	eventCmd.AddCommand(eventGetCmd)

	eventListCmd.Flags().StringSliceVar(&eventTypes, "type", nil, "filter by event type (repeatable)")
	eventListCmd.Flags().StringVar(&eventResourceType, "resource-type", "", "filter by resource type")
	eventListCmd.Flags().StringVar(&eventResourceID, "resource-id", "", "filter by resource ID")
	eventListCmd.Flags().StringVar(&eventOperator, "operator", "", "filter by operator ID or name")
	eventListCmd.Flags().DurationVar(&eventSince, "since", 0, "show events newer than this duration (e.g. 24h)")
	eventListCmd.Flags().IntVar(&eventLimit, "limit", 50, "maximum number of events to return")
	eventListCmd.Flags().StringVar(&eventCursor, "cursor", "", "pagination cursor from a previous response")
}

func runEventList(cmd *cobra.Command, args []string) error {
	printer := client.NewPrinter(GetOutputFormat())

	filter := &nisv1.EventFilter{
		Types:        eventTypes,
		ResourceType: eventResourceType,
		ResourceId:   eventResourceID,
		Limit:        int32(eventLimit),
		Cursor:       eventCursor,
	}

	if eventOperator != "" {
		opID, err := resolveOperatorID(eventOperator)
		if err != nil {
			return err
		}
		filter.OperatorId = opID
	}

	if eventSince > 0 {
		t := time.Now().Add(-eventSince)
		filter.Since = timestamppb.New(t)
	}

	resp, err := GetClient().Event.ListEvents(context.Background(), connect.NewRequest(&nisv1.ListEventsRequest{
		Filter: filter,
	}))
	if err != nil {
		return fmt.Errorf("failed to list events: %w", err)
	}

	if len(resp.Msg.Events) == 0 {
		if GetOutputFormat() != "quiet" {
			printer.PrintMessage("No events found")
		}
		return nil
	}

	if GetOutputFormat() == "table" {
		headers := []string{"TIME", "TYPE", "ACTOR", "RESOURCE"}
		rows := make([][]string, len(resp.Msg.Events))
		for i, e := range resp.Msg.Events {
			ts := "-"
			if e.OccurredAt != nil {
				ts = e.OccurredAt.AsTime().UTC().Format(time.RFC3339)
			}
			actor := "system"
			if e.ActorId != "" {
				prefix := e.ActorId
				if len(prefix) > 8 {
					prefix = prefix[:8]
				}
				actor = e.ActorType + ":" + prefix
			}
			resID := e.ResourceId
			if len(resID) > 8 {
				resID = resID[:8]
			}
			resource := e.ResourceType
			if resID != "" {
				resource = e.ResourceType + "/" + resID
			}
			rows[i] = []string{ts, e.Type, actor, resource}
		}
		if err := printer.PrintTable(headers, rows); err != nil {
			return err
		}
		if resp.Msg.NextCursor != "" && GetOutputFormat() != "quiet" {
			printer.PrintMessage("Next cursor: %s", resp.Msg.NextCursor)
		}
		return nil
	}

	return printer.PrintList(resp.Msg.Events)
}

func runEventGet(cmd *cobra.Command, args []string) error {
	id := args[0]

	resp, err := GetClient().Event.GetEvent(context.Background(), connect.NewRequest(&nisv1.GetEventRequest{
		Id: id,
	}))
	if err != nil {
		return fmt.Errorf("failed to get event: %w", err)
	}

	if GetOutputFormat() == "quiet" {
		client.NewPrinter(GetOutputFormat()).PrintID(resp.Msg.Event.Id)
		return nil
	}

	if GetOutputFormat() != "table" {
		return client.NewPrinter(GetOutputFormat()).PrintObject(resp.Msg.Event)
	}

	e := resp.Msg.Event
	ts := "-"
	if e.OccurredAt != nil {
		ts = e.OccurredAt.AsTime().UTC().Format(time.RFC3339)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "ID:            %s\n", e.Id)
	fmt.Fprintf(&sb, "Time:          %s\n", ts)
	fmt.Fprintf(&sb, "Type:          %s\n", e.Type)
	fmt.Fprintf(&sb, "Actor:         %s / %s\n", e.ActorType, e.ActorId)
	fmt.Fprintf(&sb, "Operator:      %s\n", e.OperatorId)
	fmt.Fprintf(&sb, "Account:       %s\n", e.AccountId)
	fmt.Fprintf(&sb, "Resource:      %s / %s\n", e.ResourceType, e.ResourceId)
	if e.PayloadJson != "" {
		pretty := prettyJSON(e.PayloadJson)
		fmt.Fprintf(&sb, "Payload:\n%s\n", pretty)
	}

	fmt.Print(sb.String())
	return nil
}

func prettyJSON(s string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, err := json.MarshalIndent(v, "  ", "  ")
	if err != nil {
		return s
	}
	return "  " + string(b)
}
