package spindle

import (
	"context"
	"encoding/json"
	"fmt"

	"tangled.sh/tangled.sh/core/api/tangled"
)

func (s *Spindle) exec(ctx context.Context, src string, msg []byte) error {
	pipeline := tangled.Pipeline{}
	data := map[string]any{}
	err := json.Unmarshal(msg, &data)
	if err != nil {
		fmt.Println("error unmarshalling", err)
		return err
	}

	if data["nsid"] == tangled.PipelineNSID {
		event, ok := data["event"]
		if !ok {
			s.l.Error("no event in message")
			return nil
		}

		rawEvent, err := json.Marshal(event)
		if err != nil {
			return err
		}

		err = json.Unmarshal(rawEvent, &pipeline)
		if err != nil {
			return err
		}

		rkey, ok := data["rkey"].(string)
		if !ok {
			s.l.Error("no rkey in message")
			return nil
		}

		err = s.eng.SetupPipeline(ctx, &pipeline, rkey)
		if err != nil {
			return err
		}
		err = s.eng.StartWorkflows(ctx, &pipeline, rkey)
		if err != nil {
			return err
		}
	}

	return nil
}
