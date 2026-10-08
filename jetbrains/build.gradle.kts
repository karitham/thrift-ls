import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType
import org.jetbrains.intellij.platform.gradle.TestFrameworkType

plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.20"
    id("org.jetbrains.intellij.platform") version "2.19.0"
}

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    intellijPlatform {
        // Build and test against either target IDE: -Pide=goland|rubymine.
        when (providers.gradleProperty("ide").getOrElse("goland")) {
            "goland" -> goland("2026.2.3")
            "rubymine" -> rubymine("2026.2.3")
            else -> error("ide must be goland or rubymine")
        }
        testFramework(TestFrameworkType.Platform)
    }
    testImplementation("junit:junit:4.13.2")
}

kotlin {
    jvmToolchain(21)
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            sinceBuild = "262"
        }
    }
    pluginVerification {
        ides {
            // Build and run tests once; verify the resulting ZIP on both IDEs.
            create(IntelliJPlatformType.GoLand, "2026.2.3")
            create(IntelliJPlatformType.RubyMine, "2026.2.3")
        }
    }
}
