/*
 * Copyright 2024 CloudWeGo Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package compose

import (
	"context"
	"fmt"
	"reflect"

	"github.com/cloudwego/eino/callbacks"
	icb "github.com/cloudwego/eino/internal/callbacks"
	"github.com/cloudwego/eino/schema"
	"github.com/cloudwego/eino/utils/generic"
)

func mergeMap(vs []any) (any, error) {
	typ := reflect.TypeOf(vs[0])
	merged := reflect.MakeMap(typ)
	for _, v := range vs {
		if reflect.TypeOf(v) != typ {
			return nil, fmt.Errorf(
				"(mergeMap) field type mismatch. expected: '%v', got: '%v'", typ, reflect.TypeOf(v))
		}

		iter := reflect.ValueOf(v).MapRange()
		for iter.Next() {
			key, val := iter.Key(), iter.Value()
			if merged.MapIndex(key).IsValid() {
				return nil, fmt.Errorf("(mergeMap) duplicated key ('%v') found", key.Interface())
			}
			merged.SetMapIndex(key, val)
		}
	}

	return merged.Interface(), nil
}

// the caller should ensure len(vs) > 1
func mergeValues(vs []any) (any, error) {
	v0 := reflect.ValueOf(vs[0])
	t0 := v0.Type()
	k0 := t0.Kind()

	if k0 == reflect.Map {
		return mergeMap(vs)
	}

	if s, ok := vs[0].(streamReader); ok {
		if s.getChunkType().Kind() != reflect.Map {
			return nil, fmt.Errorf("(mergeValues | stream type)"+
				" unsupported chunk type: %v", s.getChunkType())
		}

		ss := make([]streamReader, len(vs)-1)
		for i := 0; i < len(ss); i++ {
			s_, ok_ := vs[i+1].(streamReader)
			if !ok_ {
				return nil, fmt.Errorf("(mergeStream) unexpected type. "+
					"expect: %v, got: %v", t0, reflect.TypeOf(vs[i]))
			}

			if s_.getChunkType() != s.getChunkType() {
				return nil, fmt.Errorf("(mergeStream) chunk type mismatch. "+
					"expect: %v, got: %v", s.getChunkType(), s_.getChunkType())
			}

			ss[i] = s_
		}

		ms := s.merge(ss)

		return ms, nil
	}

	return nil, fmt.Errorf("(mergeValues) unsupported type: %v", t0)
}

type on[T any] func(context.Context, T) (context.Context, T)

func onStart[T any](ctx context.Context, input T) (context.Context, T) {
	return icb.On(ctx, input, icb.OnStartHandle[T], callbacks.TimingOnStart)
}

func onEnd[T any](ctx context.Context, output T) (context.Context, T) {
	return icb.On(ctx, output, icb.OnEndHandle[T], callbacks.TimingOnEnd)
}

func onStartWithStreamInput[T any](ctx context.Context, input *schema.StreamReader[T]) (
	context.Context, *schema.StreamReader[T]) {

	return icb.On(ctx, input, icb.OnStartWithStreamInputHandle[T], callbacks.TimingOnStartWithStreamInput)
}

func genericOnStartWithStreamInputHandle(ctx context.Context, input streamReader,
	runInfo *icb.RunInfo, handlers []icb.Handler) (context.Context, streamReader) {

	handlers = generic.Reverse(handlers)

	cpy := input.copy

	handle := func(handler icb.Handler, in streamReader) context.Context {
		in_, ok := unpackStreamReader[icb.CallbackInput](in)
		if !ok {
			panic("impossible")
		}

		return handler.OnStartWithStreamInput(ctx, runInfo, in_)
	}

	return icb.OnWithStreamHandle(ctx, input, handlers, cpy, handle)
}

func genericOnStartWithStreamInput(ctx context.Context, input streamReader) (context.Context, streamReader) {
	return icb.On(ctx, input, genericOnStartWithStreamInputHandle, callbacks.TimingOnStartWithStreamInput)
}

func onEndWithStreamOutput[T any](ctx context.Context, output *schema.StreamReader[T]) (
	context.Context, *schema.StreamReader[T]) {

	return icb.On(ctx, output, icb.OnEndWithStreamOutputHandle[T], callbacks.TimingOnEndWithStreamOutput)
}

func genericOnEndWithStreamOutputHandle(ctx context.Context, output streamReader,
	runInfo *icb.RunInfo, handlers []icb.Handler) (context.Context, streamReader) {

	cpy := output.copy

	handle := func(handler icb.Handler, out streamReader) context.Context {
		out_, ok := unpackStreamReader[icb.CallbackOutput](out)
		if !ok {
			panic("impossible")
		}

		return handler.OnEndWithStreamOutput(ctx, runInfo, out_)
	}

	return icb.OnWithStreamHandle(ctx, output, handlers, cpy, handle)
}

func genericOnEndWithStreamOutput(ctx context.Context, output streamReader) (context.Context, streamReader) {
	return icb.On(ctx, output, genericOnEndWithStreamOutputHandle, callbacks.TimingOnEndWithStreamOutput)
}

func onError(ctx context.Context, err error) (context.Context, error) {
	return icb.On(ctx, err, icb.OnErrorHandle, callbacks.TimingOnError)
}

func runWithCallbacks[I, O, TOption any](r func(context.Context, I, ...TOption) (O, error),
	onStart on[I], onEnd on[O], onError on[error]) func(context.Context, I, ...TOption) (O, error) {

	return func(ctx context.Context, input I, opts ...TOption) (output O, err error) {
		ctx, input = onStart(ctx, input)

		output, err = r(ctx, input, opts...)
		if err != nil {
			ctx, err = onError(ctx, err)
			return output, err
		}

		ctx, output = onEnd(ctx, output)

		return output, nil
	}
}

func invokeWithCallbacks[I, O, TOption any](i Invoke[I, O, TOption]) Invoke[I, O, TOption] {
	return runWithCallbacks(i, onStart[I], onEnd[O], onError)
}

func genericInvokeWithCallbacks(i invoke) invoke {
	return runWithCallbacks(i, onStart[any], onEnd[any], onError)
}

func streamWithCallbacks[I, O, TOption any](s Stream[I, O, TOption]) Stream[I, O, TOption] {
	return runWithCallbacks(s, onStart[I], onEndWithStreamOutput[O], onError)
}

func collectWithCallbacks[I, O, TOption any](c Collect[I, O, TOption]) Collect[I, O, TOption] {
	return runWithCallbacks(c, onStartWithStreamInput[I], onEnd[O], onError)
}

func transformWithCallbacks[I, O, TOption any](t Transform[I, O, TOption]) Transform[I, O, TOption] {
	return runWithCallbacks(t, onStartWithStreamInput[I], onEndWithStreamOutput[O], onError)
}

func genericTransformWithCallbacks(t transform) transform {
	return runWithCallbacks(t, genericOnStartWithStreamInput, genericOnEndWithStreamOutput, onError)
}

func initGraphCallbacks(ctx context.Context, info *nodeInfo, meta *executorMeta, opts ...Option) context.Context {
	ri := &callbacks.RunInfo{}
	if meta != nil {
		ri.Component = meta.component
		ri.Type = meta.componentImplType
	}

	if info != nil {
		ri.Name = info.name
	}

	var cbs []callbacks.Handler
	for i := range opts {
		if len(opts[i].handler) != 0 && len(opts[i].paths) == 0 {
			cbs = append(cbs, opts[i].handler...)
		}
	}

	return icb.AppendHandlers(ctx, ri, cbs...)
}

func initNodeCallbacks(ctx context.Context, key string, info *nodeInfo, meta *executorMeta, opts ...Option) context.Context {
	ri := &callbacks.RunInfo{}
	if meta != nil {
		ri.Component = meta.component
		ri.Type = meta.componentImplType
	}

	if info != nil {
		ri.Name = info.name
	}

	var cbs []callbacks.Handler
	for i := range opts {
		if len(opts[i].handler) != 0 {
			if len(opts[i].paths) != 0 {
				for _, k := range opts[i].paths {
					if len(k.path) == 1 && k.path[0] == key {
						cbs = append(cbs, opts[i].handler...)
						break
					}
				}
			}
		}
	}

	return icb.AppendHandlers(ctx, ri, cbs...)
}

func streamChunkConvertForCBOutput[O any](o O) (callbacks.CallbackOutput, error) {
	return o, nil
}

func streamChunkConvertForCBInput[I any](i I) (callbacks.CallbackInput, error) {
	return i, nil
}

func toAnyList[T any](in []T) []any {
	ret := make([]any, len(in))
	for i := range in {
		ret[i] = in[i]
	}
	return ret
}

type assignableType uint8

const (
	assignableTypeMustNot assignableType = iota
	assignableTypeMust
	assignableTypeMay
)

func checkAssignable(input, arg reflect.Type) assignableType {
	if arg == nil || input == nil {
		return assignableTypeMustNot
	}

	if arg == input {
		return assignableTypeMust
	}

	if arg.Kind() == reflect.Interface && input.Implements(arg) {
		return assignableTypeMust
	}
	if input.Kind() == reflect.Interface {
		if arg.Implements(input) {
			return assignableTypeMay
		}
		return assignableTypeMustNot
	}

	return assignableTypeMustNot
}

func extractOption(nodes map[string]*chanCall, opts ...Option) (map[string][]any, error) {
	optMap := map[string][]any{}
	for _, opt := range opts {
		if len(opt.paths) == 0 {
			// common, discard callback, filter option by type
			if len(opt.options) == 0 {
				continue
			}
			for name, c := range nodes {
				if c.action.optionType == nil {
					// subgraph
					optMap[name] = append(optMap[name], opt)
				} else if reflect.TypeOf(opt.options[0]) == c.action.optionType { // assume that types of options are the same
					optMap[name] = append(optMap[name], opt.options...)
				}
			}
		}
		for _, path := range opt.paths {
			if len(path.path) == 0 {
				return nil, fmt.Errorf("call option has designated an empty path")
			}

			var curNode *chanCall
			var ok bool
			if curNode, ok = nodes[path.path[0]]; !ok {
				return nil, fmt.Errorf("option has designated an unknown node: %s", path)
			}
			curNodeKey := path.path[0]

			if len(path.path) == 1 {
				if len(opt.options) == 0 {
					// sub graph common callbacks has been added to ctx in initNodeCallback and won't be passed to subgraph only pass options
					// node callback also won't be passed
					continue
				}
				if curNode.action.optionType == nil {
					nOpt := opt.deepCopy()
					nOpt.paths = []*NodePath{}
					optMap[curNodeKey] = append(optMap[curNodeKey], nOpt)
				} else {
					// designate to component
					if curNode.action.optionType != reflect.TypeOf(opt.options[0]) { // assume that types of options are the same
						return nil, fmt.Errorf("option type[%s] is different from which the designated node[%s] expects[%s]",
							reflect.TypeOf(opt.options[0]).String(), path, curNode.action.optionType.String())
					}
					optMap[curNodeKey] = append(optMap[curNodeKey], opt.options...)
				}
			} else {
				if curNode.action.optionType != nil {
					// component
					return nil, fmt.Errorf("cannot designate sub path of a component, path:%s", path)
				}
				// designate to sub graph's nodes
				nOpt := opt.deepCopy()
				nOpt.paths = []*NodePath{NewNodePath(path.path[1:]...)}
				optMap[curNodeKey] = append(optMap[curNodeKey], nOpt)
			}
		}
	}

	return optMap, nil
}

func mapToList(m map[string]any) []any {
	ret := make([]any, 0, len(m))
	for _, v := range m {
		ret = append(ret, v)
	}
	return ret
}
