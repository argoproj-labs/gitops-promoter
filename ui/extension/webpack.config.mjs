import path from 'path';

export default {
  entry: './index.tsx',
  output: {
    filename: 'extension-promoter.js',
    path: path.resolve(process.cwd(), 'dist'),
    library: { type: 'window' },
  },
  resolve: {
    extensions: ['.ts', '.tsx', '.js'],
    alias: {
      '@components-lib': path.resolve(process.cwd(), '../components-lib/src'),
      '@shared': path.resolve(process.cwd(), '../shared/src'),
    },
  },
  externals: {
    react: 'React',
    'react-dom': 'ReactDOM',
  },
  module: {
    rules: [
      {
        test: /\.tsx?$/,
        use: 'ts-loader',
        exclude: /node_modules/,
      },
      {
        test: /\.s?css$/,
        use: ['style-loader', 'css-loader', 'sass-loader'],
        exclude: /node_modules/,
      },
    ],
  },
  mode: 'production',
}; 