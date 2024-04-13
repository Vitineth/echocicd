package util

func Map[I any, O any](in []I, translator func(I) O) []O {
	result := make([]O, len(in))
	for i, v := range in {
		result[i] = translator(v)
	}
	return result
}

func Filter[I any](in []I, predicate func(I) bool) []I {
	result := make([]I, 0, len(in))
	idx := 0
	for _, v := range in {
		if predicate(v) {
			result[idx] = v
			idx++
		}
	}
	return result
}
