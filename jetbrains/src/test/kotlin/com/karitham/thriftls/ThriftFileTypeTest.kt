package com.karitham.thriftls

import com.intellij.openapi.fileTypes.FileTypeManager
import com.intellij.testFramework.fixtures.BasePlatformTestCase

class ThriftFileTypeTest : BasePlatformTestCase() {
    fun testThriftExtensionIsRegistered() {
        assertSame(ThriftFileType, FileTypeManager.getInstance().getFileTypeByExtension("thrift"))
        assertEquals("ThriftLS", ThriftFileType.language.id)
    }
}
