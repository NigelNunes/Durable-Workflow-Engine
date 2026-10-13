package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/nigelnunes/durable-flow/sdk/workflow"
)

type workflowFunc func(workflowID string, input json.RawMessage, history []workflow.HistoryEvent) ([]workflow.Command, *workflow.Failure)

type activityFunc func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)

func adaptWorkflow(fn any) (workflowFunc, error) {
	v := reflect.ValueOf(fn)
	t := reflect.TypeOf(fn)
	if err := checkFunc(t, reflect.TypeOf((*workflow.Context)(nil)).Elem(), "workflow.Context", "workflow"); err != nil {
		return nil, err
	}
	inType := t.In(1)
	return func(workflowID string, input json.RawMessage, history []workflow.HistoryEvent) ([]workflow.Command, *workflow.Failure) {
		arg, err := unmarshalArg(inType, input)
		if err != nil {
			return nil, &workflow.Failure{Message: fmt.Sprintf("decode workflow input: %s", err)}
		}
		adapted := func(ctx workflow.Context, _ struct{}) (any, error) {
			outs := v.Call([]reflect.Value{interfaceArg(t.In(0), ctx), arg})
			if err := callError(outs[1]); err != nil {
				return nil, err
			}
			return outs[0].Interface(), nil
		}
		return workflow.RunWorkflowTask(adapted, workflowID, struct{}{}, history)
	}, nil
}

func adaptActivity(fn any) (activityFunc, error) {
	v := reflect.ValueOf(fn)
	t := reflect.TypeOf(fn)
	if err := checkFunc(t, reflect.TypeOf((*context.Context)(nil)).Elem(), "context.Context", "activity"); err != nil {
		return nil, err
	}
	inType := t.In(1)
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		arg, err := unmarshalArg(inType, input)
		if err != nil {
			return nil, fmt.Errorf("decode activity input: %w", err)
		}
		outs := v.Call([]reflect.Value{interfaceArg(t.In(0), ctx), arg})
		if err := callError(outs[1]); err != nil {
			return nil, err
		}
		raw, err := json.Marshal(outs[0].Interface())
		if err != nil {
			return nil, fmt.Errorf("encode activity result: %w", err)
		}
		return raw, nil
	}, nil
}

func checkFunc(t, ctxType reflect.Type, ctxName, kind string) error {
	want := fmt.Sprintf("func(%s, In) (Out, error)", ctxName)
	if t == nil || t.Kind() != reflect.Func || t.NumIn() != 2 || t.NumOut() != 2 {
		return fmt.Errorf("%s must be %s", kind, want)
	}
	if t.In(0) != ctxType && !t.In(0).Implements(ctxType) {
		return fmt.Errorf("%s first argument must be %s", kind, ctxName)
	}
	errType := reflect.TypeOf((*error)(nil)).Elem()
	if !t.Out(1).Implements(errType) {
		return fmt.Errorf("%s must return an error", kind)
	}
	return nil
}

func unmarshalArg(t reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	ptr := reflect.New(t)
	if err := json.Unmarshal(raw, ptr.Interface()); err != nil {
		return reflect.Value{}, err
	}
	return ptr.Elem(), nil
}

func interfaceArg(param reflect.Type, value any) reflect.Value {
	arg := reflect.New(param).Elem()
	arg.Set(reflect.ValueOf(value))
	return arg
}

func callError(v reflect.Value) error {
	if v.IsNil() {
		return nil
	}
	return v.Interface().(error)
}
