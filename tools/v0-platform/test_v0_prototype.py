import base64
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import zipfile
import v0_prototype as v0


class ArtifactTests(unittest.TestCase):
    def test_text_binary_and_archive(self):
        with tempfile.TemporaryDirectory() as root:
            files = {'files': [
                {'path': 'src/App.vue', 'encoding': 'utf8', 'content': '<template>原型</template>'},
                {'path': 'public/icon.bin', 'encoding': 'base64', 'content': base64.b64encode(b'\x00\xff').decode()}
            ]}
            first = v0.save_files(files, root)
            second = v0.save_files(files, root)
            self.assertNotEqual(first['directory'], second['directory'])
            self.assertEqual((Path(first['directory']) / 'public/icon.bin').read_bytes(), b'\x00\xff')
            with zipfile.ZipFile(first['archive']) as archive:
                self.assertEqual(set(archive.namelist()), {'src/App.vue', 'public/icon.bin'})
                self.assertIn('原型', archive.read('src/App.vue').decode())

    def test_path_escape_creates_no_files(self):
        for name in ['../escape', '/absolute', 'a/../../escape', 'a\\escape', '.', '\x00bad']:
            with tempfile.TemporaryDirectory() as root:
                with self.assertRaises(ValueError):
                    v0.save_files({'files': [{'path': name, 'content': 'bad'}]}, root)
                self.assertEqual(list(Path(root).iterdir()), [])

    def test_bad_encoding_and_directory_collision(self):
        for files in [
            [{'path': 'file', 'content': '!', 'encoding': 'base64'}],
            [{'path': 'src', 'content': 'file'}, {'path': 'src/a', 'content': 'nested'}]
        ]:
            with tempfile.TemporaryDirectory() as root:
                with self.assertRaises(ValueError):
                    v0.save_files({'files': files}, root)
                self.assertEqual(list(Path(root).iterdir()), [])

    def test_preserves_handle_when_files_not_ready(self):
        with patch.object(v0, 'request', side_effect=[({'id': 'chat_x'}, 'signed-handle'), ({'files': []}, None)]):
            result = v0.invoke('v0_create', {'prompt': 'Example'})
        self.assertEqual(result['handle'], 'signed-handle')
        self.assertIn('download_error', result)


if __name__ == '__main__':
    unittest.main()
