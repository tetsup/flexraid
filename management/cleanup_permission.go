package management

import (
 "context"
 "fmt"
 "github.com/tetsup/flexraid/pkg/shared"
)

type SetDriveDeprecatedVersionDeletionPermissionRequest struct { Context context.Context; WWN string; Allowed bool }
type SetDriveDeprecatedVersionDeletionPermissionResponse struct { WWN string; Allowed bool }
func (layer *Management) SetDriveDeprecatedVersionDeletionPermission(request SetDriveDeprecatedVersionDeletionPermissionRequest) (SetDriveDeprecatedVersionDeletionPermissionResponse,error) {
 err:=layer.updateDriveCleanupPermission(request.Context,request.WWN,func(p *shared.DriveCleanupPermission){p.DeleteDeprecatedVersionsAllowed=request.Allowed})
 if err!=nil{return SetDriveDeprecatedVersionDeletionPermissionResponse{},err};return SetDriveDeprecatedVersionDeletionPermissionResponse{WWN:request.WWN,Allowed:request.Allowed},nil
}
type SetDriveAbnormalDataPreservationPermissionRequest struct { Context context.Context; WWN string; Allowed bool }
type SetDriveAbnormalDataPreservationPermissionResponse struct { WWN string; Allowed bool }
func (layer *Management) SetDriveAbnormalDataPreservationPermission(request SetDriveAbnormalDataPreservationPermissionRequest) (SetDriveAbnormalDataPreservationPermissionResponse,error) {
 err:=layer.updateDriveCleanupPermission(request.Context,request.WWN,func(p *shared.DriveCleanupPermission){p.PreserveAbnormalDataAllowed=request.Allowed})
 if err!=nil{return SetDriveAbnormalDataPreservationPermissionResponse{},err};return SetDriveAbnormalDataPreservationPermissionResponse{WWN:request.WWN,Allowed:request.Allowed},nil
}
type SetDriveLogicalObjectDeletionPermissionRequest struct { Context context.Context; WWN string; Allowed bool }
type SetDriveLogicalObjectDeletionPermissionResponse struct { WWN string; Allowed bool }
func (layer *Management) SetDriveLogicalObjectDeletionPermission(request SetDriveLogicalObjectDeletionPermissionRequest) (SetDriveLogicalObjectDeletionPermissionResponse,error) {
 err:=layer.updateDriveCleanupPermission(request.Context,request.WWN,func(p *shared.DriveCleanupPermission){p.DeleteLogicalObjectsAllowed=request.Allowed})
 if err!=nil{return SetDriveLogicalObjectDeletionPermissionResponse{},err};return SetDriveLogicalObjectDeletionPermissionResponse{WWN:request.WWN,Allowed:request.Allowed},nil
}
func (layer *Management) updateDriveCleanupPermission(ctx context.Context,wwn string,update func(*shared.DriveCleanupPermission)) error {
 if ctx==nil{return fmt.Errorf("context is required")};if err:=ctx.Err();err!=nil{return err}
 layer.cleanupPermissionUpdateMu.Lock();defer layer.cleanupPermissionUpdateMu.Unlock()
 if _,ok:=layer.drives[wwn];!ok{return fmt.Errorf("WWN %q is not configured",wwn)}
 index:=layer.cleanupDriveIndex(wwn);if index<0{return fmt.Errorf("cleanup permissions for WWN %q are not initialized",wwn)}
 snapshot:=layer.cleanupPermissionStore.Snapshot();if index>=len(snapshot.Drives){return fmt.Errorf("cleanup permission index %d is out of range",index)}
 update(&snapshot.Drives[index]);if err:=layer.cleanupPermissionStore.Replace(snapshot.Drives);err!=nil{return err}
 if layer.distribution!=nil{layer.distribution.NotifyCleanupPermissionsChanged()};return nil
}
type GetDriveCleanupPermissionsRequest struct { Context context.Context; WWN string }
type GetDriveCleanupPermissionsResponse struct { WWN string; Permissions shared.DriveCleanupPermission }
func (layer *Management) GetDriveCleanupPermissions(request GetDriveCleanupPermissionsRequest)(GetDriveCleanupPermissionsResponse,error){
 if request.Context==nil{return GetDriveCleanupPermissionsResponse{},fmt.Errorf("context is required")};if err:=request.Context.Err();err!=nil{return GetDriveCleanupPermissionsResponse{},err}
 if _,ok:=layer.drives[request.WWN];!ok{return GetDriveCleanupPermissionsResponse{},fmt.Errorf("WWN %q is not configured",request.WWN)}
 index:=layer.cleanupDriveIndex(request.WWN);snapshot:=layer.cleanupPermissionStore.Snapshot();if index<0||index>=len(snapshot.Drives){return GetDriveCleanupPermissionsResponse{},fmt.Errorf("cleanup permissions for WWN %q are not initialized",request.WWN)}
 return GetDriveCleanupPermissionsResponse{WWN:request.WWN,Permissions:snapshot.Drives[index]},nil
}
func (layer *Management) cleanupDriveIndex(wwn string)int{for i,v:=range layer.driveOrder{if v==wwn{return i}};return -1}
func (layer *Management) StopDistributionCleanupWorker(ctx context.Context)error{return layer.StopBackgroundProcesses(ctx)}
func (layer *Management) StartDistributionCleanupWorker(ctx context.Context)error{return layer.StartBackgroundProcesses(ctx)}
