package distribution

type GetErrorStatsRequest struct{}
type IOErrorStats struct { IOIndex int; Count uint64; Failure string }
type CorruptedStripe struct { ObjectID string; StripeID uint64 }
type ErrorStats struct { TotalErrors uint64; IOs []IOErrorStats; CorruptedStripes []CorruptedStripe }
type GetErrorStatsResponse struct { Stats ErrorStats }

func(d *Distribution)GetErrorStats(_ GetErrorStatsRequest)GetErrorStatsResponse{
	result:=ErrorStats{TotalErrors:d.totalErrors,IOs:make([]IOErrorStats,len(d.ioErrors))}
	for i,count:=range d.ioErrors{entry:=IOErrorStats{IOIndex:i,Count:count};if d.ioFailures[i]!=nil{entry.Failure=d.ioFailures[i].Error()};result.IOs[i]=entry}
	for objectID,stripes:=range d.objects{for stripeID,info:=range stripes{if info.corrupted{result.CorruptedStripes=append(result.CorruptedStripes,CorruptedStripe{ObjectID:objectID.String(),StripeID:uint64(stripeID)})}}}
	return GetErrorStatsResponse{Stats:result}
}
