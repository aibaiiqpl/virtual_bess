package iec61850

// #include <iec61850_server.h>
import "C"

// UpdateBooleanAttributeValue 更新布尔型 DataAttribute（如 SPS 告警点 AlmN.stVal）。
//
// 上游 go-bindings/iec61850 v1.0.0 只包装了 Float/Int32/UTCTime/VisibleString/Quality
// 的属性更新，唯独缺布尔——而 IEC61850 离散告警点 stVal 是布尔 SPS。本地 vendored 副本
// 补上这一个方法，照搬 UpdateInt32AttributeValue 的写法，调底层 C 库已有的
// IedServer_updateBooleanAttributeValue（见 iec61850_server.h:1206）。
func (is *IedServer) UpdateBooleanAttributeValue(node *ModelNode, value bool) {
	if node == nil || node._modelNode == nil {
		return
	}
	C.IedServer_updateBooleanAttributeValue(is.server, (*C.DataAttribute)(node._modelNode), C.bool(value))
}
