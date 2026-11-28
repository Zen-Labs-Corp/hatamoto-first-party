package main

import "hatamoto.dev/sdk"

// pageFilter is replaced by the render.page implementation.
func pageFilter(p sdk.PageContext) (sdk.PageContext, error) { return p, nil }
