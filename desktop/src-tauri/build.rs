fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&[
            "choose_bundle",
            "refresh_bundle",
            "change_memory",
            "preview_client",
            "change_client",
        ]),
    ))
    .expect("could not generate desktop permissions");
}
