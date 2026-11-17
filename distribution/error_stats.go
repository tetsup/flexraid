package distribution

import "sort"

type GetErrorStatsRequest struct{}
type IOErrorStats struct {
	IOIndex int
	Count uint64
	Failure string
}
type CorruptedStripe struct {
	ObjectID string
	StripeID uint64
}
type ErrorStats struct {
	TotalErrors uint64
	IOs []IOErrorStats
	CorruptedStripes []CorruptedStripe
}
type GetErrorStatsResponse struct{ Stats ErrorStats }

func (d *Distribution) GetErrorStats(_ GetErrorStatsRequest) GetErrorStatsResponse {
	d.mapMu.Lock()
	result := ErrorStats{TotalErrors:d.totalErrors, IOs:make([]IOErrorStats,len(d.ioErrors))}
	for i,count := range d.ioErrors {
		entry:=IOErrorStats{IOIndex:i,Count:count}
		if d.ioFailures[i]!=nil { entry.Failure=d.ioFailures[i].Error() }
		result.IOs[i]=entry
	}
	type stripeRef struct { objectID string; key stripeIDKey }
	refs:=make([]stripeRef,0)
	for objectID,stripes:=range d.objects {
		for stripeID:=range stripes {
			refs=append(refs,stripeRef{objectID:objectID.String(),key:stripeIDKey{objectID:objectID,stripeID:stripeID}})
		}
	}
	d.mapMu.Unlock()
	for _,ref:=range refs {
		unlock:=d.lockStripe(ref.key)
		d.mapMu.Lock()
		info:=d.objects[ref.key.objectID][ref.key.stripeID]
		corrupted:=info!=nil && info.corrupted
		d.mapMu.Unlock()
		unlock()
		if corrupted { result.CorruptedStripes=append(result.CorruptedStripes,CorruptedStripe{ObjectID:ref.objectID,StripeID:uint64(ref.key.stripeID)}) }
	}
	sort.Slice(result.CorruptedStripes,func(i,j int)bool{
		if result.CorruptedStripes[i].ObjectID!=result.CorruptedStripes[j].ObjectID{return result.CorruptedStripes[i].ObjectID<result.CorruptedStripes[j].ObjectID}
		return result.CorruptedStripes[i].StripeID<result.CorruptedStripes[j].StripeID
	})
	return GetErrorStatsResponse{Stats:result}
}
