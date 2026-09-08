package inspect

import (
	"encoding/json"
	"fmt"

	"github.com/go-rod/rod"
)

// ReactComponent represents a React component in the fiber tree.
type ReactComponent struct {
	Name     string           `json:"name"`
	Type     string           `json:"type"`
	Key      string           `json:"key,omitempty"`
	Props    json.RawMessage  `json:"props,omitempty"`
	State    json.RawMessage  `json:"state,omitempty"`
	Children []ReactComponent `json:"children,omitempty"`
}

// ReactTree extracts the React component tree from the page.
// Requires React DevTools hook (__REACT_DEVTOOLS_GLOBAL_HOOK__).
func ReactTree(page *rod.Page, maxDepth int) ([]ReactComponent, error) {
	if maxDepth <= 0 {
		maxDepth = 10
	}
	res, err := page.Eval(fmt.Sprintf(`() => {
		const hook = window.__REACT_DEVTOOLS_GLOBAL_HOOK__;
		if (!hook || !hook.renderers || hook.renderers.size === 0) return null;

		function walk(fiber, depth) {
			if (!fiber || depth > %d) return null;
			const name = fiber.type
				? (typeof fiber.type === 'string' ? fiber.type : fiber.type.displayName || fiber.type.name || 'Anonymous')
				: 'Fragment';
			const node = {
				name: name,
				type: typeof fiber.type === 'string' ? 'host' : 'composite',
				key: fiber.key || '',
			};
			if (fiber.memoizedProps && typeof fiber.type !== 'string') {
				try {
					const p = {};
					for (const [k,v] of Object.entries(fiber.memoizedProps)) {
						if (typeof v !== 'function' && typeof v !== 'object') p[k] = v;
					}
					if (Object.keys(p).length > 0) node.props = p;
				} catch(e) {}
			}
			if (fiber.memoizedState && typeof fiber.memoizedState === 'object' && fiber.memoizedState !== null) {
				try {
					const s = fiber.memoizedState.memoizedState;
					if (s !== undefined && typeof s !== 'function') node.state = s;
				} catch(e) {}
			}
			const children = [];
			let child = fiber.child;
			while (child) {
				const c = walk(child, depth + 1);
				if (c) children.push(c);
				child = child.sibling;
			}
			if (children.length > 0) node.children = children;
			return node;
		}

		const roots = [];
		hook.renderers.forEach((renderer, id) => {
			const fiberRoots = hook.getFiberRoots ? hook.getFiberRoots(id) : new Set();
			fiberRoots.forEach(root => {
				const tree = walk(root.current, 0);
				if (tree) roots.push(tree);
			});
		});
		return roots.length > 0 ? roots : null;
	}`, maxDepth))
	if err != nil {
		return nil, fmt.Errorf("react tree: %w", err)
	}
	if res.Value.Val() == nil {
		return nil, fmt.Errorf("no React detected on this page (missing __REACT_DEVTOOLS_GLOBAL_HOOK__)")
	}
	raw, err := res.Value.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var components []ReactComponent
	if err := json.Unmarshal(raw, &components); err != nil {
		return nil, fmt.Errorf("parse react tree: %w", err)
	}
	return components, nil
}

// ReactSuspense returns the current Suspense boundary states.
func ReactSuspense(page *rod.Page) ([]map[string]any, error) {
	res, err := page.Eval(`() => {
		const hook = window.__REACT_DEVTOOLS_GLOBAL_HOOK__;
		if (!hook || !hook.renderers || hook.renderers.size === 0) return null;

		const boundaries = [];
		function walk(fiber) {
			if (!fiber) return;
			if (fiber.tag === 13) {
				boundaries.push({
					fallback: !!(fiber.memoizedState),
					name: fiber.type?.displayName || fiber.type?.name || 'Suspense',
				});
			}
			walk(fiber.child);
			walk(fiber.sibling);
		}

		hook.renderers.forEach((renderer, id) => {
			const fiberRoots = hook.getFiberRoots ? hook.getFiberRoots(id) : new Set();
			fiberRoots.forEach(root => walk(root.current));
		});
		return boundaries.length > 0 ? boundaries : null;
	}`)
	if err != nil {
		return nil, fmt.Errorf("react suspense: %w", err)
	}
	if res.Value.Val() == nil {
		return nil, fmt.Errorf("no React Suspense boundaries found")
	}
	raw, err := res.Value.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var boundaries []map[string]any
	if err := json.Unmarshal(raw, &boundaries); err != nil {
		return nil, err
	}
	return boundaries, nil
}
