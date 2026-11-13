package orchestration

func containsStripeRange(ranges []stripeRange, id uint64) bool {
	for _, item := range ranges {
		if id >= item.start && id <= item.end {
			return true
		}
	}
	return false
}

func appendStripeRange(ranges []stripeRange, id uint64) []stripeRange {
	if len(ranges) > 0 {
		last := &ranges[len(ranges)-1]
		if last.end == id || (last.end != ^uint64(0) && last.end+1 == id) {
			if id > last.end {
				last.end = id
			}
			return ranges
		}
	}
	return append(ranges, stripeRange{start: id, end: id})
}

func removeStripeFromRanges(ranges []stripeRange, id uint64) []stripeRange {
	result := make([]stripeRange, 0, len(ranges)+1)
	for _, item := range ranges {
		if id < item.start || id > item.end {
			result = append(result, item)
			continue
		}
		if item.start < id {
			result = append(result, stripeRange{start: item.start, end: id - 1})
		}
		if id < item.end {
			result = append(result, stripeRange{start: id + 1, end: item.end})
		}
	}
	return result
}

func cloneStripeRanges(ranges []stripeRange) []stripeRange {
	return append([]stripeRange(nil), ranges...)
}
