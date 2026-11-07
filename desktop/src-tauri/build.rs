fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "choose_bundle",
            "refresh_bundle",
            "change_memory",
        ]),
    ))
    .expect("could not generate desktop permissions");
}
